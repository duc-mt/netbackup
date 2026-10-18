// ==============================================================================
// Package main implements Implementation and logic for logging_test..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run logging_test.go [options]
// Notes:         Go package implementation
// ==============================================================================
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
		"Total runtime: 650ms",
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

func TestDiffBlockColors(t *testing.T) {
	logDir := t.TempDir()
	logger, err := New(logDir, "text")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	var console strings.Builder
	logger.out = &console
	logger.SetColor(true)

	logger.DiffBlock("vyos1", "  ", "+interface eth1 address 1.1.1.1\n-interface eth1 address 2.2.2.2\n context line")
	logger.Close()

	out := console.String()
	if !strings.Contains(out, colorAdd+"+interface eth1 address 1.1.1.1"+colorReset) && !strings.Contains(out, colorAdd+"  +interface eth1 address 1.1.1.1"+colorReset) {
		t.Errorf("expected green color code for '+' line, got: %q", out)
	}
	if !strings.Contains(out, colorDel+"-interface eth1 address 2.2.2.2"+colorReset) && !strings.Contains(out, colorDel+"  -interface eth1 address 2.2.2.2"+colorReset) {
		t.Errorf("expected red color code for '-' line, got: %q", out)
	}
	if !strings.Contains(out, "[vyos1]") {
		t.Errorf("expected host tag [vyos1] in console output: %q", out)
	}

	// File must NOT contain ANSI escape codes
	entries, _ := os.ReadDir(logDir)
	content, _ := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	fileStr := string(content)
	if strings.Contains(fileStr, "\x1b[") {
		t.Errorf("log file must not contain ANSI escape codes, got: %q", fileStr)
	}
	if !strings.Contains(fileStr, "+interface eth1 address 1.1.1.1") {
		t.Errorf("log file missing '+' line: %s", fileStr)
	}
	if !strings.Contains(fileStr, "-interface eth1 address 2.2.2.2") {
		t.Errorf("log file missing '-' line: %s", fileStr)
	}
}

func TestLoggingDryRun(t *testing.T) {
	logDir := t.TempDir()

	logger, err := New(logDir, "text")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	results := []backup.Result{
		{
			Device:   inventory.Device{Hostname: "sw-dry", Address: "10.0.0.99"},
			Success:  true,
			DryRun:   true,
			Duration: 42 * time.Millisecond,
		},
	}

	failures := logger.Summary(results)
	if failures != 0 {
		t.Errorf("expected 0 failures, got %d", failures)
	}
	logger.Close()

	entries, _ := os.ReadDir(logDir)
	content, _ := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	logStr := string(content)

	if !strings.Contains(logStr, "OK      sw-dry               (10.0.0.99) [DRY-RUN]") {
		t.Errorf("log missing expected dry-run line: %s", logStr)
	}
	if !strings.Contains(logStr, "Devices: 1 total, 1 succeeded, 0 failed") {
		t.Errorf("log missing expected totals: %s", logStr)
	}
}

func TestLoggingProgress(t *testing.T) {
	logDir := t.TempDir()

	logger, err := New(logDir, "text")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	resDry := backup.Result{
		Device:   inventory.Device{Hostname: "sw-dry", Address: "10.0.0.1"},
		Success:  true,
		DryRun:   true,
		Duration: 50 * time.Millisecond,
	}
	resSaved := backup.Result{
		Device:      inventory.Device{Hostname: "sw-saved", Address: "10.0.0.2"},
		Success:     true,
		OutputPath:  "backups/test/sw-saved.cfg",
		DiffAdded:   3,
		DiffRemoved: 1,
		Duration:    150 * time.Millisecond,
	}
	resFail := backup.Result{
		Device:   inventory.Device{Hostname: "sw-fail", Address: "10.0.0.3"},
		Success:  false,
		Category: errcat.CategoryAuthFailed,
		Err:      errors.New("bad password"),
		Duration: 80 * time.Millisecond,
	}

	logger.Progress(1, 3, resDry)
	logger.Progress(2, 3, resSaved)
	logger.Progress(3, 3, resFail)
	logger.Close()

	entries, _ := os.ReadDir(logDir)
	content, _ := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	logStr := string(content)

	expected := []string{
		"[INFO] [1/3] OK      sw-dry               (10.0.0.1) [DRY-RUN]",
		"[INFO] [2/3] OK      sw-saved             (10.0.0.2) [SAVED (+3/-1)] -> backups/test/sw-saved.cfg",
		"[ERROR] [3/3] FAILED  sw-fail              (10.0.0.3) category=AUTHENTICATION_FAILED detail=bad password",
	}
	for _, exp := range expected {
		if !strings.Contains(logStr, exp) {
			t.Errorf("log file does not contain %q\nFull log:\n%s", exp, logStr)
		}
	}
}
