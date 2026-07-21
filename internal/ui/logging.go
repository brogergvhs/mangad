package ui

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Log is the logging interface the app consumes.
// carries printf-style methods and slog-style structured methods.
type Log interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)

	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)

	With(args ...any) Log
}

// Logger is the slog-backed implementation of Log.
type Logger struct {
	s *slog.Logger
}

// Options configures a Logger. Format is "text" (default) or "json".
type Options struct {
	Debug  bool
	Format string
	Writer io.Writer // defaults to os.Stderr
}

// New builds a structured logger. Level is Info, or Debug when opts.Debug is set.
func New(opts Options) *Logger {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}
	level := slog.LevelInfo
	if opts.Debug {
		level = slog.LevelDebug
	}
	ho := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(opts.Format, "json") {
		h = slog.NewJSONHandler(w, ho)
	} else {
		h = slog.NewTextHandler(w, ho)
	}
	return &Logger{s: slog.New(h)}
}

// NewLogger is the original constructor, kept for callers/tests that only toggle
// debug and want the default format.
func NewLogger(debug bool) *Logger { return New(Options{Debug: debug}) }

// Discard returns a logger that drops everything (tests, quiet code paths).
func Discard() *Logger { return New(Options{Writer: io.Discard}) }

func (l *Logger) With(args ...any) Log { return &Logger{s: l.s.With(args...)} }

func (l *Logger) Debug(msg string, args ...any) { l.s.Debug(msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.s.Info(msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.s.Warn(msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.s.Error(msg, args...) }

// logf renders a printf-style message into the record's msg.
func (l *Logger) logf(level slog.Level, format string, args ...any) {
	if !l.s.Enabled(context.Background(), level) {
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	l.s.Log(context.Background(), level, msg)
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(slog.LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(slog.LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(slog.LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(slog.LevelError, format, args...) }

// RedirectStdLog routes the standard library logger (log.Printf, ...) through l
// at Warn level.
func RedirectStdLog(l *Logger) {
	log.SetFlags(0)
	log.SetOutput(stdLogWriter{l: l})
}

type stdLogWriter struct{ l *Logger }

func (w stdLogWriter) Write(p []byte) (int, error) {
	w.l.s.Log(context.Background(), slog.LevelWarn, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
