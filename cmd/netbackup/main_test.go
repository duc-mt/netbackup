// ==============================================================================
// Package main implements tests for the CLI wiring of the analysis features.
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-12
// License:       MIT
// ==============================================================================
package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"netbackup/internal/backup"
	"netbackup/internal/logging"
	"netbackup/internal/memory"
	"netbackup/internal/report"
)

// newCLITestLogger returns a logger writing into a temp dir; logText reads
// back what it wrote.
func newCLITestLogger(t *testing.T) (*logging.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := logging.New(dir, "text")
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return logger, dir
}

func logText(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no log file in %s (err=%v)", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestConfigureSemanticDiffDisabled(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	configureSemanticDiff(logger, &cfg, cliOptions{ignoreVolatile: false})

	if cfg.VolatileFilter != nil {
		t.Error("no filter should be installed when -ignore-volatile=false")
	}
	if !strings.Contains(logText(t, dir), "semantic diff disabled") {
		t.Error("expected the log to say semantic diff is disabled")
	}
}

func TestConfigureSemanticDiffBuiltinRules(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	configureSemanticDiff(logger, &cfg, cliOptions{ignoreVolatile: true})

	if cfg.VolatileFilter == nil || cfg.VolatileFilter.Len() != backup.DefaultVolatileFilter().Len() {
		t.Fatalf("expected the built-in filter, got %v", cfg.VolatileFilter)
	}
	if !strings.Contains(logText(t, dir), "semantic diff: ignoring volatile lines") {
		t.Error("expected the log to announce the active filter")
	}
}

func TestConfigureSemanticDiffExtraPatterns(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	patterns := filepath.Join(t.TempDir(), "volatile.txt")
	if err := os.WriteFile(patterns, []byte("# site specific\n^# build id \\d+\n(unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configureSemanticDiff(logger, &cfg, cliOptions{ignoreVolatile: true, volatilePatterns: patterns})

	if want := backup.DefaultVolatileFilter().Len() + 1; cfg.VolatileFilter.Len() != want {
		t.Errorf("filter has %d rules, want %d (built-ins + the one valid custom pattern)", cfg.VolatileFilter.Len(), want)
	}
	if got := cfg.VolatileFilter.Apply("no-such-vendor", "# build id 7\nreal"); got != "real" {
		t.Errorf("custom pattern not active: %q", got)
	}
	if !strings.Contains(logText(t, dir), "pattern ignored") {
		t.Error("the invalid pattern should be reported as ignored")
	}
}

func TestConfigureSemanticDiffMissingPatternsFile(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	configureSemanticDiff(logger, &cfg, cliOptions{ignoreVolatile: true, volatilePatterns: filepath.Join(t.TempDir(), "missing.txt")})

	if cfg.VolatileFilter == nil || cfg.VolatileFilter.Len() != backup.DefaultVolatileFilter().Len() {
		t.Error("an unreadable patterns file must fall back to the built-in rules")
	}
	if !strings.Contains(logText(t, dir), "continuing with the built-in rules only") {
		t.Error("expected a warning about the unreadable patterns file")
	}
}

func TestConfigureComplianceOffByDefault(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	configureCompliance(logger, &cfg, cliOptions{})

	if cfg.Baseline != nil {
		t.Error("compliance must stay disabled without -golden-dir / -compliance-rules")
	}
	if strings.Contains(logText(t, dir), "compliance") {
		t.Error("nothing should be logged when compliance is not requested")
	}
}

func TestConfigureComplianceLoadsBaseline(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	golden := t.TempDir()
	if err := os.WriteFile(filepath.Join(golden, "vyos.golden"), []byte("set service lldp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configureCompliance(logger, &cfg, cliOptions{goldenDir: golden})

	if cfg.Baseline == nil || cfg.Baseline.Len() != 1 {
		t.Fatalf("expected a baseline with 1 rule, got %v", cfg.Baseline)
	}
	if !strings.Contains(logText(t, dir), "checking devices against 1 golden baseline rule(s)") {
		t.Error("expected the log to announce the baseline")
	}
}

func TestConfigureComplianceBadPathDisablesChecksWithoutFailing(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	var cfg backup.Config

	configureCompliance(logger, &cfg, cliOptions{goldenDir: filepath.Join(t.TempDir(), "missing")})

	if cfg.Baseline != nil {
		t.Error("an unusable baseline must leave compliance disabled")
	}
	text := logText(t, dir)
	if !strings.Contains(text, "compliance:") || !strings.Contains(text, "compliance checking disabled") {
		t.Errorf("expected the load error and the disabled notice in the log:\n%s", text)
	}
}

func sampleRows() []report.BackupResult {
	return []report.BackupResult{
		{
			HostnameIP: "sw1 (10.0.0.1)", Platform: "cisco_ios", Status: "SUCCESS", Diff: "+1/-0",
			Risk: "WARNING", RiskBadge: "VLAN", DiffLines: []string{"+vlan 100"},
		},
		{HostnameIP: "sw2 (10.0.0.2)", Platform: "cisco_ios", Status: "UNCHANGED", Diff: "-"},
	}
}

func TestWriteReportsWritesBothFilesWithPlaceholders(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	out := t.TempDir()

	opts := cliOptions{
		htmlReport: filepath.Join(out, "{run}", "report.html"),
		mdReport:   filepath.Join(out, "reports", "CHANGELOG.md"),
	}
	writeReports(logger, opts, "backup-test-run", sampleRows())

	html, err := os.ReadFile(filepath.Join(out, "backup-test-run", "report.html"))
	if err != nil {
		t.Fatalf("HTML report not written at the expanded path: %v", err)
	}
	if !strings.Contains(string(html), "<!DOCTYPE html>") || !strings.Contains(string(html), "sw1 (10.0.0.1)") {
		t.Error("HTML report content is incomplete")
	}

	md, err := os.ReadFile(filepath.Join(out, "reports", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("Markdown report not written: %v", err)
	}
	if !strings.Contains(string(md), "backup-test-run") || !strings.Contains(string(md), "```diff") {
		t.Error("Markdown report content is incomplete")
	}

	text := logText(t, dir)
	if !strings.Contains(text, "HTML report written to") || !strings.Contains(text, "Markdown report written to") {
		t.Errorf("expected both reports to be announced in the log:\n%s", text)
	}
}

func TestWriteReportsWithoutFlagsDoesNothing(t *testing.T) {
	logger, dir := newCLITestLogger(t)
	writeReports(logger, cliOptions{}, "backup-test-run", sampleRows())
	if strings.Contains(logText(t, dir), "report") {
		t.Error("no report flags were given, nothing should be logged")
	}
}

func TestWriteReportsFailureIsOnlyAWarning(t *testing.T) {
	logger, dir := newCLITestLogger(t)

	// A regular file in the middle of the path makes the directory creation fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := cliOptions{
		htmlReport: filepath.Join(blocker, "sub", "report.html"),
		mdReport:   filepath.Join(blocker, "sub", "CHANGELOG.md"),
	}
	writeReports(logger, opts, "backup-test-run", sampleRows()) // must not panic

	text := logText(t, dir)
	if !strings.Contains(text, "HTML report not written") || !strings.Contains(text, "Markdown report not written") {
		t.Errorf("expected a warning per failed report:\n%s", text)
	}
}

func TestEmitReportRecoversFromPanic(t *testing.T) {
	logger, dir := newCLITestLogger(t)

	emitReport(logger, "HTML", "x.html", func() ([]string, error) { panic("boom") })

	if !strings.Contains(logText(t, dir), "HTML report generation failed: boom") {
		t.Error("a panicking report writer must be logged, not propagated")
	}
}

func TestEmitReportLogsWarningsAndOutcome(t *testing.T) {
	logger, dir := newCLITestLogger(t)

	emitReport(logger, "HTML", "ok.html", func() ([]string, error) { return []string{"row 3 skipped"}, nil })
	emitReport(logger, "Markdown", "bad.md", func() ([]string, error) { return nil, errors.New("disk full") })

	text := logText(t, dir)
	for _, want := range []string{
		"row 3 skipped",
		"HTML report written to ok.html",
		"Markdown report not written to bad.md: disk full",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q:\n%s", want, text)
		}
	}
}

func TestCLIOptionsDefaults(t *testing.T) {
	opts := parseFlags()
	if opts.retries < 0 {
		t.Errorf("retries should default to >= 0, got %d", opts.retries)
	}
}

func TestRouteMemoryIntegration(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "mem.json")
	s, err := memory.Load(tmpFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := s.Record("vyos1", "10.0.0.1", "jump1", "10.0.0.254:22", "auto-fallback"); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	rec, ok := s.Get("10.0.0.1")
	if !ok || rec.JumpHost != "jump1" {
		t.Fatalf("expected jump1, got %+v", rec)
	}
}
