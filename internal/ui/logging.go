package ui

import (
	"fmt"
)

// Log is the logging interface the app consumes; *Logger is the
// terminal implementation.
type Log interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Errorf(format string, args ...any)
}

type Logger struct {
	Debug bool
}

func NewLogger(debug bool) *Logger {
	return &Logger{Debug: debug}
}

func (l *Logger) Debugf(format string, args ...any) {
	if l.Debug {
		fmt.Printf("[DEBUG] "+format, args...)
	}
}

func (l *Logger) Infof(format string, args ...any) {
	fmt.Printf("[INFO] "+format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	fmt.Printf("[ERROR] "+format, args...)
}
