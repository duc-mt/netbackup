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

	colorCritical = "\x1b[1;97;41m" // risk marker / alert: CRITICAL (bold white on red)
	colorWarning  = "\x1b[1;33m"    // risk marker / alert: WARNING (bold yellow)
)

const (
	// maxAlertFindings caps how many classified lines the run summary lists
	// per device in the risk alerts block (the full diff is in the log).
	maxAlertFindings = 8

	// maxLoggedDrifts caps how many compliance drifts are listed per device.
	maxLoggedDrifts = 10
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
	if err := os.MkdirAll(logDir, 0o700); err != nil {
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

func (l *Logger) line(level, msg string) { l.emit(level, msg, "") }

// emit writes one log line. The log file always receives plain text;
// the console copy is wrapped in color (text format only) when color is
// non-empty and console colouring is enabled.
func (l *Logger) emit(level, msg, color string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ts := time.Now().Format(time.RFC3339)
	var entry, consoleEntry string
	if l.format == "json" {
		b, _ := json.Marshal(logEntry{
			Timestamp: ts,
			Level:     level,
			Message:   msg,
		})
		entry = string(b) + "\n"
		consoleEntry = entry
	} else {
		entry = fmt.Sprintf("%s [%s] %s\n", ts, level, msg)
		consoleEntry = entry
		if l.color && color != "" {
			consoleEntry = fmt.Sprintf("%s [%s] %s%s%s\n", ts, level, color, msg, colorReset)
		}
	}
	fmt.Fprint(l.out, consoleEntry)
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
//
// Added and removed lines that the risk classifier recognises as CRITICAL
// or WARNING are logged at WARN level and tagged with a "<== CRITICAL: ACL"
// style marker (colored on the console, plain in the log file), so
// security-relevant changes stand out inside a long diff.
func (l *Logger) DiffLines(host, prefix string, lines []string) {
	if len(lines) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	for _, rawLine := range lines {
		mark, markColor := riskMarker(rawLine)
		level := "INFO"
		if mark != "" {
			level = "WARN"
		}

		tagFile := ""
		if host != "" {
			tagFile = "[" + host + "] "
		}
		if l.format == "json" {
			b, _ := json.Marshal(logEntry{
				Timestamp: now.Format(time.RFC3339),
				Level:     level,
				Message:   fmt.Sprintf("%s%s%s%s", tagFile, prefix, rawLine, mark),
			})
			fmt.Fprintln(l.f, string(b))
		} else {
			fmt.Fprintf(l.f, "%s [%s] %s%s%s%s\n", now.Format(time.RFC3339), level, tagFile, prefix, rawLine, mark)
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
				if mark != "" {
					lineOut += markColor + mark + colorReset
				}
				fmt.Fprintf(l.out, "%s [%s] %s%s\n", now.Format(time.RFC3339), level, tagConsole, lineOut)
			} else {
				tagConsole := ""
				if host != "" {
					tagConsole = "[" + host + "] "
				}
				fmt.Fprintf(l.out, "%s [%s] %s%s%s%s\n", now.Format(time.RFC3339), level, tagConsole, prefix, rawLine, mark)
			}
		}
	}
}

// riskMarker returns the inline marker (and its console color) for a diff
// line, or empty strings when the line is not CRITICAL or WARNING.
func riskMarker(rawLine string) (mark, color string) {
	f, ok := backup.ClassifyLine(rawLine)
	if !ok {
		return "", ""
	}
	switch f.Level {
	case backup.RiskCritical:
		return "  <== CRITICAL: " + f.Category, colorCritical
	case backup.RiskWarning:
		return "  <== WARNING: " + f.Category, colorWarning
	default:
		return "", ""
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

// Progress logs real-time status as each device completes during a backup run.
func (l *Logger) Progress(completed, total int, r backup.Result) {
	if r.Success {
		if r.DryRun {
			l.Info("[%d/%d] OK      %-20s (%s) [DRY-RUN] [%s]", completed, total, r.Device.Hostname, r.Device.Address, r.Duration.Round(time.Millisecond))
			return
		}
		status := "SAVED"
		if r.Unchanged {
			status = "UNCHANGED"
			if r.VolatileOnly {
				status = "UNCHANGED (volatile lines only)"
			}
		} else if r.HasDiff || r.DiffAdded > 0 || r.DiffRemoved > 0 {
			status = fmt.Sprintf("SAVED (+%d/-%d)", r.DiffAdded, r.DiffRemoved)
		}
		if r.Risk.Level != backup.RiskNone {
			status += " RISK=" + riskLabel(r.Risk)
		}
		l.Info("[%d/%d] OK      %-20s (%s) [%s] -> %s [%s]", completed, total, r.Device.Hostname, r.Device.Address, status, r.OutputPath, r.Duration.Round(time.Millisecond))
		return
	}
	l.Error("[%d/%d] FAILED  %-20s (%s) category=%s detail=%v [%s]", completed, total, r.Device.Hostname, r.Device.Address, r.Category, r.Err, r.Duration.Round(time.Millisecond))
}

// Summary prints a per-device outcome line for every result plus roll-up
// counts by failure category, and returns the number of failures so the
// caller can set a meaningful process exit code.
//
// Beyond the outcome lines it also reports, when present: per-device
// warnings (a skipped or failed analysis step), golden-config compliance
// drifts, and a "Risk alerts" block listing the security- and
// routing-relevant changes of the run.
func (l *Logger) Summary(results []backup.Result, totalRuntime ...time.Duration) int {
	failures := 0
	byCategory := make(map[errcat.Category]int)

	l.Info("---- Backup run summary ----")
	for _, r := range results {
		if r.Success {
			if r.DryRun {
				l.Info("OK      %-20s (%s) [DRY-RUN] [%s]", r.Device.Hostname, r.Device.Address, r.Duration.Round(time.Millisecond))
				l.deviceNotes(r)
				continue
			}
			status := "SAVED"
			if r.Unchanged {
				status = "UNCHANGED"
				if r.VolatileOnly {
					status = "UNCHANGED (volatile lines only)"
				}
			} else if r.HasDiff || r.DiffAdded > 0 || r.DiffRemoved > 0 {
				status = fmt.Sprintf("SAVED (+%d/-%d)", r.DiffAdded, r.DiffRemoved)
			}
			if r.Risk.Level != backup.RiskNone {
				status += " RISK=" + riskLabel(r.Risk)
			}
			l.Info("OK      %-20s (%s) [%s] -> %s [%s]", r.Device.Hostname, r.Device.Address, status, r.OutputPath, r.Duration.Round(time.Millisecond))
			l.deviceNotes(r)
			continue
		}
		failures++
		byCategory[r.Category]++
		l.Error("FAILED  %-20s (%s) category=%s detail=%v [%s]", r.Device.Hostname, r.Device.Address, r.Category, r.Err, r.Duration.Round(time.Millisecond))
		l.deviceNotes(r)
	}

	var totalDur time.Duration
	if len(totalRuntime) > 0 && totalRuntime[0] > 0 {
		totalDur = totalRuntime[0]
	} else {
		for _, r := range results {
			totalDur += r.Duration
		}
	}

	l.Info("---- Totals ----")
	l.Info("Devices: %d total, %d succeeded, %d failed", len(results), len(results)-failures, failures)
	if totalDur > 0 {
		l.Info("Total runtime: %s", totalDur.Round(time.Millisecond))
	}
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

	l.riskAlerts(results)
	l.complianceTotals(results)

	return failures
}

// riskLabel renders an assessment for the run log, e.g. "CRITICAL(ACL,SSH)".
func riskLabel(a backup.RiskAssessment) string {
	if len(a.Categories) == 0 {
		return a.Level.String()
	}
	return a.Level.String() + "(" + strings.Join(a.Categories, ",") + ")"
}

// deviceNotes logs a device's analysis warnings and compliance drifts.
func (l *Logger) deviceNotes(r backup.Result) {
	for _, w := range r.Warnings {
		l.Warn("WARN    %-20s %s", r.Device.Hostname, w)
	}
	drifts := r.Compliance.Drifts
	if !r.Compliance.Checked || len(drifts) == 0 {
		return
	}
	l.Warn("DRIFT   %-20s %s (%d of %d baseline rule(s) failed)", r.Device.Hostname, r.Compliance.Summary(), len(drifts), r.Compliance.Rules)
	for i, d := range drifts {
		if i >= maxLoggedDrifts {
			l.Warn("DRIFT   %-20s   ... and %d more", r.Device.Hostname, len(drifts)-maxLoggedDrifts)
			break
		}
		l.Warn("DRIFT   %-20s   %s", r.Device.Hostname, d.String())
	}
}

// riskAlerts logs a prominent block for every device whose changes were
// classified WARNING or CRITICAL, plus a one-line count of devices per
// highest risk level. It prints nothing when no device carries a risk.
func (l *Logger) riskAlerts(results []backup.Result) {
	var critical, warning, info int
	for _, r := range results {
		switch r.Risk.Level {
		case backup.RiskCritical:
			critical++
		case backup.RiskWarning:
			warning++
		case backup.RiskInfo:
			info++
		}
	}
	if critical+warning+info == 0 {
		return
	}

	if critical+warning > 0 {
		l.emit("WARN", "---- Risk alerts ----", colorWarning)
		for _, r := range results {
			if r.Risk.Level != backup.RiskCritical && r.Risk.Level != backup.RiskWarning {
				continue
			}
			color := colorWarning
			if r.Risk.Level == backup.RiskCritical {
				color = colorCritical
			}
			l.emit("WARN", fmt.Sprintf("RISK    %-20s %s (+%d/-%d)", r.Device.Hostname, riskLabel(r.Risk), r.DiffAdded, r.DiffRemoved), color)

			shown := 0
			notable := r.Risk.Count(backup.RiskCritical) + r.Risk.Count(backup.RiskWarning)
			for _, f := range r.Risk.Findings {
				if f.Level != backup.RiskCritical && f.Level != backup.RiskWarning {
					continue
				}
				if shown == maxAlertFindings {
					l.Warn("          ... and %d more notable line(s)", notable-shown)
					break
				}
				l.Warn("          %-8s %-11s %s", f.Level, f.Category, f.Line)
				shown++
			}
		}
	}
	l.Info("Risk: %d critical, %d warning, %d info (devices by highest risk level)", critical, warning, info)
}

// complianceTotals logs how many checked devices match their golden
// baseline. It prints nothing when no device was checked.
func (l *Logger) complianceTotals(results []backup.Result) {
	checked, compliant := 0, 0
	for _, r := range results {
		if !r.Compliance.Checked {
			continue
		}
		checked++
		if r.Compliance.Compliant() {
			compliant++
		}
	}
	if checked == 0 {
		return
	}
	l.Info("Compliance: %d of %d checked device(s) match the golden baseline (%.0f%%)", compliant, checked, 100*float64(compliant)/float64(checked))
}
