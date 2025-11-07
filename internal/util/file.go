package util

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
)

func CreateCBZ(files []string, output string) error {
	out, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("cbz: %w", err)
	}

	defer func() {
		if cerr := out.Close(); cerr != nil {
			log.Printf("error closing output file %s: %v", output, cerr)
		}
	}()

	z := zip.NewWriter(out)
	defer func() {
		if cerr := z.Close(); cerr != nil {
			log.Printf("error closing zip writer for %s: %v", output, cerr)
		}
	}()

	sort.Strings(files)
	for _, file := range files {
		if err := addFileToZip(z, file); err != nil {
			return err
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
