// ==============================================================================
// Package logging implements tests for risk highlighting in run logs.
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-12
// License:       MIT
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
	"netbackup/internal/compliance"
	"netbackup/internal/errcat"
	"netbackup/internal/inventory"
)

// newRiskTestLogger returns a logger whose console output is captured.
func newRiskTestLogger(t *testing.T, format string, color bool) (*Logger, *strings.Builder, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := New(dir, format)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var console strings.Builder
	logger.out = &console
	logger.SetColor(color)
	t.Cleanup(func() { _ = logger.Close() })
	return logger, &console, dir
}

func readLogFile(t *testing.T, dir string) string {
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

func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, text)
	return ""
}

func TestRiskMarker(t *testing.T) {
	tests := []struct {
		line      string
		wantMark  string
		wantColor string
	}{
		{"+aaa new-model", "  <== CRITICAL: AAA", colorCritical},
		{"-set service ssh port '22'", "  <== CRITICAL: SSH", colorCritical},
		{"+router bgp 65001", "  <== WARNING: ROUTING", colorWarning},
		{"+vlan 10", "  <== WARNING: VLAN", colorWarning},
		{"+ntp server 1.1.1.1", "", ""},             // INFO: no marker
		{"+interface eth1 address 1.1.1.1", "", ""}, // unrecognised
		{" aaa new-model", "", ""},                  // context line
	}
	for _, tt := range tests {
		mark, color := riskMarker(tt.line)
		if mark != tt.wantMark || color != tt.wantColor {
			t.Errorf("riskMarker(%q) = (%q, %q), want (%q, %q)", tt.line, mark, color, tt.wantMark, tt.wantColor)
		}
	}
}

func TestDiffLinesMarksRiskyLinesPlain(t *testing.T) {
	logger, console, dir := newRiskTestLogger(t, "text", false)

	logger.DiffLines("gw1", "  ", []string{
		"+set service ssh port '2222'",
		"-set protocols static route 10.0.0.0/8 next-hop 1.1.1.1",
		"+ntp server 1.1.1.1",
		"+interface eth1 address 1.1.1.1",
	})

	out := console.String()
	if l := lineContaining(t, out, "ssh port"); !strings.Contains(l, "<== CRITICAL: SSH") || !strings.Contains(l, "[WARN]") {
		t.Errorf("critical line should be a WARN with a CRITICAL marker, got %q", l)
	}
	if l := lineContaining(t, out, "static route"); !strings.Contains(l, "<== WARNING: ROUTING") {
		t.Errorf("warning line should carry a WARNING marker, got %q", l)
	}
	if l := lineContaining(t, out, "ntp server"); strings.Contains(l, "<==") || !strings.Contains(l, "[INFO]") {
		t.Errorf("INFO-level change must stay an unmarked INFO line, got %q", l)
	}
	if l := lineContaining(t, out, "interface eth1"); strings.Contains(l, "<==") {
		t.Errorf("unrecognised line must not be marked, got %q", l)
	}

	file := readLogFile(t, dir)
	if l := lineContaining(t, file, "ssh port"); !strings.Contains(l, "<== CRITICAL: SSH") || !strings.Contains(l, "[WARN]") {
		t.Errorf("log file line should carry the marker too, got %q", l)
	}
}

func TestDiffLinesRiskMarkerColors(t *testing.T) {
	logger, console, dir := newRiskTestLogger(t, "text", true)

	logger.DiffLines("gw1", "", []string{"+set service ssh port '2222'", "+router bgp 65001"})

	out := console.String()
	if !strings.Contains(out, colorCritical+"  <== CRITICAL: SSH"+colorReset) {
		t.Errorf("expected a colored CRITICAL marker, got %q", out)
	}
	if !strings.Contains(out, colorWarning+"  <== WARNING: ROUTING"+colorReset) {
		t.Errorf("expected a colored WARNING marker, got %q", out)
	}
	// The diff line itself keeps its own +/- color, uninterrupted by the marker.
	if !strings.Contains(out, colorAdd+"+set service ssh port '2222'"+colorReset) {
		t.Errorf("expected the added line to stay green, got %q", out)
	}
	if file := readLogFile(t, dir); strings.Contains(file, "\x1b[") {
		t.Errorf("log file must stay free of ANSI codes: %q", file)
	}
}

func TestDiffLinesJSONRiskLevel(t *testing.T) {
	logger, _, dir := newRiskTestLogger(t, "json", false)

	logger.DiffLines("gw1", "", []string{"+set service ssh port '2222'", "+ntp server 1.1.1.1"})

	var sawCritical, sawInfo bool
	for _, raw := range strings.Split(strings.TrimSpace(readLogFile(t, dir)), "\n") {
		var e logEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("log line is not valid JSON: %q (%v)", raw, err)
		}
		switch {
		case strings.Contains(e.Message, "ssh port"):
			sawCritical = true
			if e.Level != "WARN" || !strings.Contains(e.Message, "<== CRITICAL: SSH") {
				t.Errorf("critical entry = %+v, want level WARN with marker", e)
			}
		case strings.Contains(e.Message, "ntp server"):
			sawInfo = true
			if e.Level != "INFO" || strings.Contains(e.Message, "<==") {
				t.Errorf("info entry = %+v, want level INFO without marker", e)
			}
		}
	}
	if !sawCritical || !sawInfo {
		t.Errorf("missing entries: critical=%v info=%v", sawCritical, sawInfo)
	}
}

func TestSummaryReportsRiskComplianceAndWarnings(t *testing.T) {
	logger, console, dir := newRiskTestLogger(t, "text", false)

	results := []backup.Result{
		{
			Device:      inventory.Device{Hostname: "sw1", Address: "10.0.0.1"},
			Success:     true,
			HasDiff:     true,
			DiffAdded:   2,
			DiffRemoved: 1,
			OutputPath:  "backups/run/cisco_ios/sw1.cfg",
			Risk:        backup.AssessRisk([]string{"+ip access-list extended MGMT", "+ permit ip any any", "-ntp server 1.1.1.1"}),
			Compliance: compliance.Report{
				Checked: true,
				Rules:   2,
				Drifts:  []compliance.Drift{{Kind: compliance.KindMissing, Rule: "ntp server 1.1.1.1"}},
			},
			Warnings: []string{"change detection failed: boom"},
			Duration: 50 * time.Millisecond,
		},
		{
			Device:       inventory.Device{Hostname: "sw2", Address: "10.0.0.2"},
			Success:      true,
			Unchanged:    true,
			VolatileOnly: true,
			OutputPath:   "backups/old/cisco_ios/sw2.cfg",
			Compliance:   compliance.Report{Checked: true, Rules: 2},
		},
		{
			Device:      inventory.Device{Hostname: "gw3", Address: "10.0.0.3"},
			Success:     true,
			HasDiff:     true,
			DiffAdded:   1,
			DiffRemoved: 0,
			OutputPath:  "backups/run/vyos/gw3.set",
			Risk:        backup.AssessRisk([]string{"+router bgp 65001"}),
		},
		{
			Device:   inventory.Device{Hostname: "sw4", Address: "10.0.0.4"},
			Err:      errors.New("dial timeout"),
			Category: errcat.CategoryUnknown,
		},
	}

	if failures := logger.Summary(results); failures != 1 {
		t.Fatalf("Summary returned %d failures, want 1", failures)
	}

	for _, out := range []string{console.String(), readLogFile(t, dir)} {
		for _, want := range []string{
			"[SAVED (+2/-1) RISK=CRITICAL(ACL)]",
			"[UNCHANGED (volatile lines only)]",
			"[SAVED (+1/-0) RISK=WARNING(ROUTING)]",
			"change detection failed: boom",
			"DRIFT   sw1",
			"1 DRIFT (1 of 2 baseline rule(s) failed)",
			`missing: "ntp server 1.1.1.1"`,
			"---- Risk alerts ----",
			"RISK    sw1",
			"RISK    gw3",
			"CRITICAL ACL         +ip access-list extended MGMT",
			"WARNING  ROUTING     +router bgp 65001",
			"Risk: 1 critical, 1 warning, 0 info",
			"Compliance: 1 of 2 checked device(s) match the golden baseline (50%)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("summary output missing %q:\n%s", want, out)
			}
		}
		// INFO findings are not alert-worthy and must not be listed.
		if strings.Contains(out, "-ntp server 1.1.1.1") && strings.Contains(out, "INFO     NTP") {
			t.Errorf("INFO findings must not appear in the risk alerts block:\n%s", out)
		}
	}
}

func TestSummaryWithoutRiskOrComplianceStaysQuiet(t *testing.T) {
	logger, console, _ := newRiskTestLogger(t, "text", false)

	logger.Summary([]backup.Result{{
		Device:     inventory.Device{Hostname: "sw1", Address: "10.0.0.1"},
		Success:    true,
		OutputPath: "backups/run/cisco_ios/sw1.cfg",
	}})

	out := console.String()
	for _, unwanted := range []string{"Risk alerts", "Risk:", "Compliance:", "DRIFT", "RISK="} {
		if strings.Contains(out, unwanted) {
			t.Errorf("summary should not mention %q when there is nothing to report:\n%s", unwanted, out)
		}
	}
}

func TestSummaryCapsAlertFindingsAndDrifts(t *testing.T) {
	logger, console, _ := newRiskTestLogger(t, "text", false)

	var lines []string
	for i := 0; i < maxAlertFindings+3; i++ {
		lines = append(lines, "+ip access-list extended ACL"+string(rune('A'+i)))
	}
	var drifts []compliance.Drift
	for i := 0; i < maxLoggedDrifts+2; i++ {
		drifts = append(drifts, compliance.Drift{Kind: compliance.KindMissing, Rule: "rule " + string(rune('a'+i))})
	}

	logger.Summary([]backup.Result{{
		Device:     inventory.Device{Hostname: "sw1", Address: "10.0.0.1"},
		Success:    true,
		HasDiff:    true,
		OutputPath: "x.cfg",
		Risk:       backup.AssessRisk(lines),
		Compliance: compliance.Report{Checked: true, Rules: 99, Drifts: drifts},
	}})

	out := console.String()
	if !strings.Contains(out, "... and 3 more notable line(s)") {
		t.Errorf("expected the alert findings to be capped with a remainder note:\n%s", out)
	}
	if !strings.Contains(out, "... and 2 more") {
		t.Errorf("expected the drift list to be capped with a remainder note:\n%s", out)
	}
}

func TestEmitWritesPlainFileAndColoredConsole(t *testing.T) {
	logger, console, dir := newRiskTestLogger(t, "text", true)

	logger.emit("WARN", "something notable", colorWarning)
	logger.emit("INFO", "plain", "")

	if out := console.String(); !strings.Contains(out, colorWarning+"something notable"+colorReset) {
		t.Errorf("console should be colored, got %q", out)
	}
	file := readLogFile(t, dir)
	if strings.Contains(file, "\x1b[") || !strings.Contains(file, "[WARN] something notable") {
		t.Errorf("log file should be plain text, got %q", file)
	}
}
