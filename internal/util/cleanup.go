package util

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

func CleanupUnfinishedTempFolders(outputDir string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasSuffix(name, "_tmp") {
			full := filepath.Join(outputDir, name)

			if err := os.RemoveAll(full); err != nil {
				log.Printf("error cleaning up %s: %v", full, err)
			} else {
				log.Printf("removed unfinished temp folder %s", full)
			}
		}
	}
}

func RemoveIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	if len(entries) == 0 {
		if err := os.Remove(dir); err == nil {
			log.Printf("removed empty output folder %s", dir)
		}
	}
}

func CleanupFolder(folder string) {
	_ = os.RemoveAll(folder)
}
