package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/util"
)

const thumbHeight = 160 // row thumbs render at 56px; 160 covers retina

// makeThumb downscales an image to a small JPEG. Formats without a decoder
// (avif) return an error; callers fall back to the raw image.
func makeThumb(r io.Reader) ([]byte, string, error) {
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, "", err
	}
	b := src.Bounds()
	if b.Dy() <= 0 {
		return nil, "", fmt.Errorf("empty image")
	}
	h := thumbHeight
	w := b.Dx() * h / b.Dy()
	if b.Dy() <= h {
		h, w = b.Dy(), b.Dx()
	}
	if w < 1 {
		w = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80}); err != nil {
		return nil, "", err
	}
	return out.Bytes(), "image/jpeg", nil
}

// firstPageThumb extracts and downscales a CBZ's first page.
func firstPageThumb(cbzPath string) ([]byte, string, error) {
	zr, err := zip.OpenReader(cbzPath)
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()
	entries := util.CBZImageEntries(zr.File)
	if len(entries) == 0 {
		return nil, "", fmt.Errorf("no pages in %s", cbzPath)
	}
	rc, err := entries[0].Open()
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	return makeThumb(io.LimitReader(rc, 32<<20))
}

// generateVolumeThumbs fills in missing generated thumbnails for a title.
func generateVolumeThumbs(ctx context.Context, repo *library.Repository, titleID int64) error {
	vols, err := repo.VolumesMissingThumbs(ctx, titleID)
	if err != nil {
		return err
	}
	for _, v := range vols {
		blob, mime, err := firstPageThumb(v.File)
		if err != nil {
			continue // avif or unreadable: the cover endpoint streams the raw page
		}
		if err := repo.SetVolumeThumb(ctx, v.ID, blob, mime); err != nil {
			return err
		}
	}
	return nil
}

func (s *LibraryService) GenerateVolumeThumbs(ctx context.Context, titleID int64) error {
	return generateVolumeThumbs(ctx, s.repo, titleID)
}
