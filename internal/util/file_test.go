package util

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateCBZ(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	page := filepath.Join(dir, "page_001.jpg")
	if err := os.WriteFile(page, []byte("image-bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "chapter.cbz")

	if err := CreateCBZ([]string{page}, output, false); err != nil {
		t.Fatalf("CreateCBZ() error = %v", err)
	}
	r, err := zip.OpenReader(output)
	if err != nil {
		t.Fatalf("open cbz: %v", err)
	}
	defer r.Close()
	if len(r.File) != 1 || r.File[0].Name != "page_001.jpg" {
		t.Fatalf("cbz contents = %v, want [page_001.jpg]", r.File)
	}
	if _, err := os.Stat(output + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestCreateCBZBrokenFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "gone.jpg")
	output := filepath.Join(dir, "chapter.cbz")

	if err := CreateCBZ([]string{missing}, output, false); err == nil {
		t.Fatal("CreateCBZ() error = nil, want add failure")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed CreateCBZ left an output file")
	}
	if _, err := os.Stat(output + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("failed CreateCBZ left a temp file")
	}

	// skipBroken tolerates unreadable files.
	if err := CreateCBZ([]string{missing}, output, true); err != nil {
		t.Fatalf("CreateCBZ(skipBroken) error = %v", err)
	}
}
