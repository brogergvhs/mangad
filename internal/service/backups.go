package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

// BackupInfo is one user-data backup file managed by the app.
type BackupInfo struct {
	Name    string
	Size    int64
	ModTime time.Time
}

func (s *JobService) BackupDir() string {
	return filepath.Join(filepath.Dir(s.dbPath), "backups")
}

func (s *JobService) CreateBackup(ctx context.Context) (BackupInfo, error) {
	name := "kaodoku-" + time.Now().Format("20060102-150405") + ".db"
	path := filepath.Join(s.BackupDir(), name)
	if err := database.BackupUserData(ctx, s.dbPath, path); err != nil {
		return BackupInfo{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{Name: name, Size: info.Size(), ModTime: info.ModTime()}, nil
}

func (s *JobService) ListBackups(ctx context.Context) ([]BackupInfo, error) {
	_ = ctx
	entries, err := os.ReadDir(s.BackupDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, BackupInfo{Name: entry.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

func (s *JobService) BackupPath(name string) (string, error) {
	name, err := cleanBackupName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.BackupDir(), name), nil
}

func (s *JobService) RestoreBackup(ctx context.Context, name string) error {
	path, err := s.BackupPath(name)
	if err != nil {
		return err
	}
	return database.RestoreUserData(ctx, s.dbPath, path)
}

func (s *JobService) DeleteBackup(ctx context.Context, name string) error {
	_ = ctx
	path, err := s.BackupPath(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *JobService) UploadBackup(ctx context.Context, name string, src io.Reader) error {
	_ = ctx
	name, err := cleanBackupName(name)
	if err != nil {
		return err
	}
	dir := s.BackupDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, copyErr := io.Copy(tmp, src)
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpName)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return closeErr
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}

func cleanBackupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid backup name")
	}
	return name, nil
}
