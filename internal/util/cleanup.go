package util

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func SetupInterruptHandler(outputDir string) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sig
		fmt.Println("\nInterrupt received. Cleaning up...")

		CleanupUnfinishedTempFolders(outputDir)
		RemoveIfEmpty(outputDir)
		fmt.Println("\nExiting due to interrupt.")

		os.Exit(1)
	}()
}

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
				fmt.Printf("Error cleaning up %s: %v\n", full, err)
			} else {
				fmt.Printf("Removed %s\n", full)
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
			fmt.Printf("Removed empty output folder: %s\n", dir)
		}
	}
}

func CleanupFolder(folder string) {
	_ = os.RemoveAll(folder)
}
