package service

import "github.com/brogergvhs/kaodoku/internal/ui"

// TerminalProgressManager adapts the terminal progress UI to service progress.
type TerminalProgressManager struct {
	inner *ui.MPBProgressManager
}

// NewTerminalProgressManager creates a terminal progress manager.
func NewTerminalProgressManager() *TerminalProgressManager {
	return &TerminalProgressManager{inner: ui.NewProgressManager()}
}

// Register creates a progress handle for one chapter.
func (m *TerminalProgressManager) Register(prefix string) ProgressHandle {
	return m.inner.Register(prefix)
}

// Close waits for all terminal progress bars to finish.
func (m *TerminalProgressManager) Close() {
	m.inner.Close()
}
