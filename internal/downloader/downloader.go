package downloader

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Downloader struct {
	client     *http.Client
	debug      bool
	outputDir  string
	skipBroken bool
	retryDelay time.Duration
}

// ProgressHandle receives image download progress updates.
type ProgressHandle interface {
	Update(done, total int, bytes int64)
	MarkDone()
}

func New(c *http.Client, debug bool, outputDir string, skipBroken bool) *Downloader {
	return &Downloader{
		client:     c,
		debug:      debug,
		outputDir:  outputDir,
		skipBroken: skipBroken,
		retryDelay: time.Second,
	}
}

type chapterState struct {
	mu          sync.Mutex
	doneImages  int
	totalImages int
	doneBytes   int64
}

func (d *Downloader) DownloadImagesConcurrently(
	ctx context.Context,
	urls []string,
	folder string,
	referer string,
	maxParallel int,
	ph ProgressHandle,
) ([]string, int64, error) {
	if err := os.MkdirAll(folder, 0755); err != nil {
		return nil, 0, err
	}

	total := len(urls)
	if maxParallel < 1 {
		maxParallel = 1
	}
	if maxParallel > total && total > 0 {
		maxParallel = total
	}

	cs := &chapterState{totalImages: total}
	ph.Update(0, total, 0)

	var filesMu sync.Mutex
	files := make([]string, 0, len(urls))
	errs := make([]error, 0, 4)

	jobs := make(chan int)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for i := range jobs {
			u := urls[i]
			low := strings.ToLower(u)

			if strings.HasSuffix(low, ".gif") {
				cs.mu.Lock()
				cs.doneImages++
				ph.Update(cs.doneImages, cs.totalImages, cs.doneBytes)
				cs.mu.Unlock()
				continue
			}

			ext := imageExt(u)
			if ext == "" {
				ext = ".jpg"
			}

			path := filepath.Join(folder, fmt.Sprintf("page_%03d%s", i+1, ext))
			var last int64

			progress := func(done int64) {
				delta := done - last
				if delta <= 0 {
					return
				}

				last = done
				cs.mu.Lock()
				cs.doneBytes += delta
				ph.Update(cs.doneImages, cs.totalImages, cs.doneBytes)
				cs.mu.Unlock()
			}

			if err := d.downloadWithRetry(ctx, u, path, referer, progress); err != nil {
				_ = os.Remove(path)
				cs.mu.Lock()
				errs = append(errs, fmt.Errorf("image %d: %v", i+1, err))
				cs.doneImages++
				ph.Update(cs.doneImages, cs.totalImages, cs.doneBytes)
				cs.mu.Unlock()

				continue
			}

			filesMu.Lock()
			files = append(files, path)
			filesMu.Unlock()

			cs.mu.Lock()
			cs.doneImages++
			ph.Update(cs.doneImages, cs.totalImages, cs.doneBytes)
			cs.mu.Unlock()
		}
	}

	wg.Add(maxParallel)
	for w := 0; w < maxParallel; w++ {
		go worker()
	}

	for i := range urls {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			ph.MarkDone()
			return files, cs.doneBytes, ctx.Err()
		case jobs <- i:
		}
	}

	close(jobs)
	wg.Wait()
	ph.MarkDone()

	if len(errs) > 0 && !d.skipBroken {
		return files, cs.doneBytes, fmt.Errorf("failed %d/%d images: %s (use --skip-broken to continue)", len(errs), total, sampleErrors(errs, 3))
	}

	return files, cs.doneBytes, nil
}

func sampleErrors(errs []error, limit int) string {
	if len(errs) == 0 {
		return ""
	}
	if limit > len(errs) {
		limit = len(errs)
	}
	var b strings.Builder
	for i := range limit {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(errs[i].Error())
	}
	if len(errs) > limit {
		fmt.Fprintf(&b, "; ...")
	}
	return b.String()
}

func (d *Downloader) downloadWithRetry(
	ctx context.Context,
	url string,
	output string,
	referer string,
	progress func(done int64),
) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		err = d.download(ctx, url, output, referer, progress)
		if err == nil {
			return nil
		}

		if attempt == 3 {
			break
		}
		delay := time.Duration(attempt) * d.retryDelay
		if delay <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return err
}

func (d *Downloader) download(
	ctx context.Context,
	u, output, referer string,
	progress func(done int64),
) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Referer", referer)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}

	var bodyCloseErr error
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && bodyCloseErr == nil {
			bodyCloseErr = cerr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
			if !genericBinaryMIME(mt) || imageExt(u) == "" {
				return fmt.Errorf("unexpected MIME: %s", ct)
			}
		}
	}

	f, err := os.Create(output)
	if err != nil {
		return err
	}

	var fileCloseErr error
	defer func() {
		if cerr := f.Close(); cerr != nil && fileCloseErr == nil {
			fileCloseErr = cerr
		}
	}()

	written, err := copyWithProgress(f, resp.Body, progress)
	if err != nil {
		return err
	}

	if progress != nil && resp.ContentLength > 0 && written < resp.ContentLength {
		progress(resp.ContentLength)
	}

	if fileCloseErr != nil {
		return fileCloseErr
	}

	return bodyCloseErr
}

func imageExt(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u != nil {
		return cleanImageExt(path.Ext(u.Path))
	}
	return cleanImageExt(filepath.Ext(rawURL))
}

func cleanImageExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return ext
	default:
		return ""
	}
}

func genericBinaryMIME(mt string) bool {
	switch strings.ToLower(mt) {
	case "application/octet-stream", "binary/octet-stream", "application/x-download", "application/force-download":
		return true
	default:
		return false
	}
}
