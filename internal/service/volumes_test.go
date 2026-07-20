package service

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMoveFileFallsBackOnEXDEV(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.cbz")
	dst := filepath.Join(dir, "dst.cbz")
	if err := os.WriteFile(src, []byte("volume"), 0o640); err != nil {
		t.Fatal(err)
	}
	oldRename := renameFile
	renameFile = func(_, _ string) error { return syscall.EXDEV }
	defer func() { renameFile = oldRename }()

	if err := moveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists or stat failed: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "volume" {
		t.Fatalf("dst = %q", got)
	}
}
