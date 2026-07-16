package util

import (
	"archive/zip"
	"bytes"
	"testing"
)

// Nested archives (001/01.jpg ... 002/01.jpg) must keep folder-major page
// order; basename-only comparison ties across folders and shuffles pages.
func TestCBZImageEntriesNestedOrder(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"002/01.jpg", "001/02.jpg", "ComicInfo.xml", "001/01.jpg", "001/10.jpg", "002/02.jpg"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, f := range CBZImageEntries(zr.File) {
		got = append(got, f.Name)
	}
	want := []string{"001/01.jpg", "001/02.jpg", "001/10.jpg", "002/01.jpg", "002/02.jpg"}
	if len(got) != len(want) {
		t.Fatalf("entries = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
