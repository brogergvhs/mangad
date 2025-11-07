package ui

import (
	"fmt"
)

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
