package logging

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"netbackup/internal/backup"
	"netbackup/internal/errcat"
	"netbackup/internal/inventory"
)

func TestLogging(t *testing.T) {
	logDir := t.TempDir()

	logger, err := New(logDir, "text")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	logger.Info("info message %d", 1)
	logger.Warn("warn message %s", "warning")
	logger.Error("error message: %v", errors.New("sample error"))

	results := []backup.Result{
		{
			Device:     inventory.Device{Hostname: "sw-01", Address: "10.0.0.1"},
			Success:    true,
			Unchanged:  false,
			OutputPath: "backups/run1/cisco/sw-01.cfg",
			Duration:   100 * time.Millisecond,
		},
		{
			Device:     inventory.Device{Hostname: "sw-02", Address: "10.0.0.2"},
			Success:    true,
			Unchanged:  true,
			OutputPath: "backups/run0/cisco/sw-02.cfg",
			Duration:   50 * time.Millisecond,
		},
		{
			Device:   inventory.Device{Hostname: "fw-01", Address: "10.0.0.3"},
			Success:  false,
			Category: errcat.CategoryAuthFailed,
			Err:      errors.New("bad password"),
			Duration: 200 * time.Millisecond,
		},
		{
			Device:   inventory.Device{Hostname: "rtr-01", Address: "10.0.0.4"},
			Success:  false,
			Category: errcat.CategoryTimeout,
			Err:      errors.New("command timed out"),
			Duration: 300 * time.Millisecond,
		},
	}

	failures := logger.Summary(results)
	if failures != 2 {
		t.Errorf("expected 2 failures, got %d", failures)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}

	// Verify log file content
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("reading logDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, found %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}

	logStr := string(content)
	expectedStrings := []string{
		"[INFO] info message 1",
		"[WARN] warn message warning",
		"[ERROR] error message: sample error",
		"---- Backup run summary ----",
		"OK      sw-01                (10.0.0.1) [SAVED]",
		"OK      sw-02                (10.0.0.2) [UNCHANGED]",
		"FAILED  fw-01                (10.0.0.3) category=AUTHENTICATION_FAILED",
		"FAILED  rtr-01               (10.0.0.4) category=COMMAND_TIMEOUT",
		"Devices: 4 total, 2 succeeded, 2 failed",
		"AUTHENTICATION_FAILED               1",
		"COMMAND_TIMEOUT                     1",
	}

	for _, exp := range expectedStrings {
		if !strings.Contains(logStr, exp) {
			t.Errorf("log file does not contain %q\nFull log:\n%s", exp, logStr)
		}
	}
}

func TestLoggingJSON(t *testing.T) {
	logDir := t.TempDir()

	logger, err := New(logDir, "json")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	logger.Info("info message %d", 1)
	logger.Close()

	entries, _ := os.ReadDir(logDir)
	content, _ := os.ReadFile(filepath.Join(logDir, entries[0].Name()))

	var entry logEntry
	if err := json.Unmarshal(content, &entry); err != nil {
		t.Fatalf("failed to parse JSON log line: %v\nLine: %s", err, string(content))
	}
	if entry.Level != "INFO" {
		t.Errorf("expected level INFO, got %s", entry.Level)
	}
	if entry.Message != "info message 1" {
		t.Errorf("expected message 'info message 1', got %s", entry.Message)
	}
}
