package ui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// jsonLogger returns a JSON logger writing into buf so records can be asserted.
func jsonLogger(buf *bytes.Buffer, debug bool) *Logger {
	return New(Options{Debug: debug, Format: "json", Writer: buf})
}

func TestStructuredRecordAndNewlineTrim(t *testing.T) {
	var buf bytes.Buffer
	l := jsonLogger(&buf, false)
	// printf-style call with a trailing newline (as many old call sites have)
	l.Infof("hello %s\n", "world")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not one JSON record: %v (%q)", err, buf.String())
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if rec["msg"] != "hello world" { // trailing \n trimmed, slog adds its own
		t.Errorf("msg = %q, want %q", rec["msg"], "hello world")
	}
	if strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") != 0 {
		t.Errorf("expected a single line, got %q", buf.String())
	}
}

func TestWithAttachesAttributes(t *testing.T) {
	var buf bytes.Buffer
	l := jsonLogger(&buf, false)
	l.With("job_id", 42, "job_type", "refresh").Warnf("boom")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("bad record: %v", err)
	}
	if rec["job_id"] != float64(42) || rec["job_type"] != "refresh" {
		t.Errorf("missing With attrs: %v", rec)
	}
	if rec["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", rec["level"])
	}
}

func TestDebugGatedByLevel(t *testing.T) {
	var buf bytes.Buffer
	jsonLogger(&buf, false).Debugf("suppressed")
	if buf.Len() != 0 {
		t.Errorf("debug should be suppressed when debug=false, got %q", buf.String())
	}
	buf.Reset()
	jsonLogger(&buf, true).Debugf("shown")
	if !strings.Contains(buf.String(), "shown") {
		t.Errorf("debug should appear when debug=true, got %q", buf.String())
	}
}
