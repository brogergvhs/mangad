package util

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
)

// CreateCBZ zips files into output, writing to a temp file first so a crash
// or failed write never leaves a partial archive at the final path. With
// skipBroken, files that cannot be added are logged and skipped.
func CreateCBZ(files []string, output string, skipBroken bool) (err error) {
	tmp := output + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("cbz: %w", err)
	}
	z := zip.NewWriter(out)
	defer func() {
		// Close order matters: the zip central directory, then the file.
		err = errors.Join(err, z.Close(), out.Close())
		if err != nil {
			_ = os.Remove(tmp)
			return
		}
		err = os.Rename(tmp, output)
	}()

	sort.Strings(files)
	for _, file := range files {
		if addErr := addFileToZip(z, file); addErr != nil {
			if !skipBroken {
				return fmt.Errorf("cbz: add %s: %w", file, addErr)
			}
			log.Printf("warning: skipping %s: %v", file, addErr)
		}
	}

	return nil
}

func addFileToZip(z *zip.Writer, file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			log.Printf("error closing input file %s: %v", file, cerr)
		}
	}()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}

	header.Name = filepath.Base(file)
	header.Method = zip.Deflate

	w, err := z.CreateHeader(header)
	if err != nil {
		return err
	}

	if _, err := io.Copy(w, f); err != nil {
		return err
	}

	return nil
}
