// ==============================================================================
// Package main implements Implementation and logic for logging..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run logging.go [options]
// Notes:         Go package implementation
// ==============================================================================
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
	"sync"
	"time"

	"golang.org/x/term"

	"netbackup/internal/backup"
	"netbackup/internal/errcat"
)

const (
	colorReset = "\x1b[0m"
	colorHost  = "\x1b[36m"
	colorAdd   = "\x1b[32m" // DiffBlock: a "+" (addition) line, coloured like `git diff`
	colorDel   = "\x1b[31m" // DiffBlock: a "-" (removal) line
)

type Logger struct {
	mu     sync.Mutex
	out    io.Writer // console (stderr)
	f      *os.File  // log file, kept open for the run's duration
	format string
	color  bool
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
	colorize := os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stderr.Fd()))
	return &Logger{out: os.Stderr, f: f, format: strings.ToLower(format), color: colorize}, nil
}

func (l *Logger) Close() error { return l.f.Close() }

// SetColor enables or disables ANSI color output on the console writer.
func (l *Logger) SetColor(color bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.color = color
}

type logEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

func (l *Logger) line(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

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

// DiffBlock outputs unified-diff-style text: each line starting with "+"
// is shown in green and one starting with "-" in red on color terminals,
// matching netconfig's diff highlighting. The log file always gets plain,
// uncolored text.
func (l *Logger) DiffBlock(host, prefix, text string) {
	raw := splitLines(text)
	l.DiffLines(host, prefix, raw)
}

// DiffLines formats and writes diff lines with +/- color highlighting on console.
func (l *Logger) DiffLines(host, prefix string, lines []string) {
	if len(lines) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for _, rawLine := range lines {
		tagFile := ""
		if host != "" {
			tagFile = "[" + host + "] "
		}
		if l.format == "json" {
			b, _ := json.Marshal(logEntry{
				Timestamp: now.Format(time.RFC3339),
				Level:     "INFO",
				Message:   fmt.Sprintf("%s%s%s", tagFile, prefix, rawLine),
			})
			fmt.Fprintln(l.f, string(b))
		} else {
			fmt.Fprintf(l.f, "%s [INFO] %s%s%s\n", now.Format(time.RFC3339), tagFile, prefix, rawLine)
		}

		if l.out != nil {
			if l.color {
				tagConsole := ""
				if host != "" {
					tagConsole = colorHost + "[" + host + "]" + colorReset + " "
				}
				lineColor := ""
				switch {
				case strings.HasPrefix(rawLine, "+"):
					lineColor = colorAdd
				case strings.HasPrefix(rawLine, "-"):
					lineColor = colorDel
				}
				lineOut := prefix + rawLine
				if lineColor != "" {
					lineOut = lineColor + prefix + rawLine + colorReset
				}
				fmt.Fprintf(l.out, "%s [INFO] %s%s\n", now.Format(time.RFC3339), tagConsole, lineOut)
			} else {
				tagConsole := ""
				if host != "" {
					tagConsole = "[" + host + "] "
				}
				fmt.Fprintf(l.out, "%s [INFO] %s%s%s\n", now.Format(time.RFC3339), tagConsole, prefix, rawLine)
			}
		}
	}
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

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
			} else if r.HasDiff || r.DiffAdded > 0 || r.DiffRemoved > 0 {
				status = fmt.Sprintf("SAVED (+%d/-%d)", r.DiffAdded, r.DiffRemoved)
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
