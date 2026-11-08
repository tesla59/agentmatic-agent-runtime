package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestSetup_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(Config{
		Level:  "debug",
		Format: "text",
		Output: &buf,
	})

	l.Debug("debug message", "key", "val")
	output := buf.String()

	if !strings.Contains(output, "level=DEBUG") {
		t.Errorf("expected level=DEBUG in output, got: %s", output)
	}
	if !strings.Contains(output, "msg=\"debug message\"") {
		t.Errorf("expected msg in output, got: %s", output)
	}
	if !strings.Contains(output, "key=val") {
		t.Errorf("expected key=val in output, got: %s", output)
	}
}

func TestSetup_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(Config{
		Level:  "info",
		Format: "json",
		Output: &buf,
	})

	l.Info("json message", "count", 42)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON log: %v", err)
	}

	if entry["level"] != "INFO" {
		t.Errorf("expected level INFO, got %v", entry["level"])
	}
	if entry["msg"] != "json message" {
		t.Errorf("expected msg 'json message', got %v", entry["msg"])
	}
	if entry["count"] != float64(42) {
		t.Errorf("expected count 42, got %v", entry["count"])
	}
}

func TestSetup_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := Setup(Config{
		Level:  "warn",
		Format: "text",
		Output: &buf,
	})

	l.Info("this should be ignored")
	if buf.Len() != 0 {
		t.Errorf("expected info message to be filtered out at warn level, got: %s", buf.String())
	}

	l.Warn("this should appear")
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("expected warn message, got: %s", buf.String())
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "json")

	cfg := ConfigFromEnv()
	if cfg.Level != "error" {
		t.Errorf("expected level 'error', got %q", cfg.Level)
	}
	if cfg.Format != "json" {
		t.Errorf("expected format 'json', got %q", cfg.Format)
	}

	l := Setup(cfg)
	if !l.Enabled(t.Context(), slog.LevelError) {
		t.Error("expected error level enabled")
	}
	if l.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("expected info level disabled at error level")
	}
}
