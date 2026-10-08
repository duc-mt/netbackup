// Package logging provides minimal, dependency-free leveled logging plus an
// end-of-run summary -- what an operator needs to answer "what ran, what
// failed, and why" without grepping raw stack traces.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"netbackup/internal/backup"
	"netbackup/internal/errcat"
)

type Logger struct {
	out    io.Writer // console (stderr)
	f      *os.File  // log file, kept open for the run's duration
	format string
}

// New creates the log directory if needed and opens a timestamped log file
// for this run, writing every line to both the file and the console.
func New(logDir, format string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}
	name := fmt.Sprintf("backup_run_%s.log", time.Now().Format("20060102-150405"))
	f, err := os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}
	if format == "" {
		format = "text"
	}
	return &Logger{out: os.Stderr, f: f, format: strings.ToLower(format)}, nil
}

func (l *Logger) Close() error { return l.f.Close() }

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func (l *Logger) line(level, msg string) {
	var entry string
	if l.format == "json" {
		b, _ := json.Marshal(logEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			Level:     level,
			Message:   msg,
		})
		entry = string(b) + "\n"
	} else {
		entry = fmt.Sprintf("%s [%s] %s\n", time.Now().Format(time.RFC3339), level, msg)
	}
	fmt.Fprint(l.out, entry)
	fmt.Fprint(l.f, entry)
}

func (l *Logger) Info(format string, args ...any)  { l.line("INFO", fmt.Sprintf(format, args...)) }
func (l *Logger) Warn(format string, args ...any)  { l.line("WARN", fmt.Sprintf(format, args...)) }
func (l *Logger) Error(format string, args ...any) { l.line("ERROR", fmt.Sprintf(format, args...)) }

// Summary prints a per-device outcome line for every result plus roll-up
// counts by failure category, and returns the number of failures so the
// caller can set a meaningful process exit code.
func (l *Logger) Summary(results []backup.Result) int {
	failures := 0
	byCategory := make(map[errcat.Category]int)

	l.Info("---- Backup run summary ----")
	for _, r := range results {
		if r.Success {
			status := "SAVED"
			if r.Unchanged {
				status = "UNCHANGED"
			}
			l.Info("OK      %-20s (%s) [%s] -> %s [%s]", r.Device.Hostname, r.Device.Address, status, r.OutputPath, r.Duration.Round(time.Millisecond))
			continue
		}
		failures++
		byCategory[r.Category]++
		l.Error("FAILED  %-20s (%s) category=%s detail=%v [%s]", r.Device.Hostname, r.Device.Address, r.Category, r.Err, r.Duration.Round(time.Millisecond))
	}

	l.Info("---- Totals ----")
	l.Info("Devices: %d total, %d succeeded, %d failed", len(results), len(results)-failures, failures)
	if failures > 0 {
		cats := make([]string, 0, len(byCategory))
		for c := range byCategory {
			cats = append(cats, string(c))
		}
		sort.Strings(cats)
		for _, c := range cats {
			l.Info("  %-35s %d", c, byCategory[errcat.Category(c)])
		}
	}

	return failures
}
