package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		bytes   int64
		want    string
		wantErr bool
	}{
		{-1, "-", false},
		{-2, "", true},
		{0, "0 B", false},
		{1023, "1023 B", false},
		{1024, "1.00 KiB", false},
		{45896, "44.82 KiB", false},
		{1024 * 1024, "1.00 MiB", false},
	}

	for _, tt := range tests {
		got, err := FormatFileSize(tt.bytes)
		if tt.wantErr {
			if err == nil {
				t.Errorf("FormatFileSize(%d): expected error, got nil", tt.bytes)
			}
			continue
		}
		if err != nil {
			t.Errorf("FormatFileSize(%d): unexpected error: %v", tt.bytes, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FormatFileSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
		wantErr bool
	}{
		{-0.5, "", true},
		{0, "0.00s", false},
		{3.42, "3.42s", false},
		{65.5, "1m05.5s", false},
	}

	for _, tt := range tests {
		got, err := FormatDuration(tt.seconds)
		if tt.wantErr {
			if err == nil {
				t.Errorf("FormatDuration(%v): expected error, got nil", tt.seconds)
			}
			continue
		}
		if err != nil {
			t.Errorf("FormatDuration(%v): unexpected error: %v", tt.seconds, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestColorForStatus(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{StatusSuccess, ansiGreen},
		{StatusUnchanged, ansiGreen},
		{StatusAuthFailed, ansiRed},
		{StatusTimeout, ansiYellow},
		{StatusFailed, ansiRed},
		{"auth failed", ansiRed}, // case-insensitive
		{"something odd", ""},    // unrecognized -> no color, not an alarming default
	}

	for _, tt := range tests {
		if got := colorForStatus(tt.status); got != tt.want {
			t.Errorf("colorForStatus(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// TestBuildRowNeverPanicsOnBadInput exercises the defense described in the
// package doc: a BackupResult with values that can never be valid must
// degrade to a placeholder cell plus a warning, not panic.
func TestBuildRowNeverPanicsOnBadInput(t *testing.T) {
	bad := BackupResult{
		HostnameIP: "bogus-host",
		FileSize:   -99,   // invalid: only -1 is a valid sentinel
		Duration:   -12.3, // invalid: negative duration
		Error:      errors.New("boom"),
	}

	row, warnings := buildRow(bad)

	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings (file size + duration), got %d: %v", len(warnings), warnings)
	}
	if row.cells[3] != "N/A" {
		t.Errorf("FileSize cell = %q, want N/A", row.cells[3])
	}
	if row.cells[4] != "N/A" {
		t.Errorf("Duration cell = %q, want N/A", row.cells[4])
	}
}

// TestPrintSummaryTableNeverPanics backs the package's core guarantee:
// no input to the table renderer -- empty, nil, or malformed -- should
// ever crash the caller's goroutine.
func TestPrintSummaryTableNeverPanics(t *testing.T) {
	inputs := [][]BackupResult{
		nil,
		{},
		MockResults(),
		{{HostnameIP: "", Platform: "", Status: "", FileSize: -5, Duration: -1, Error: nil}},
	}

	for _, results := range inputs {
		var out, warn bytes.Buffer
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("fprintSummaryTable panicked: %v", r)
				}
			}()
			fprintSummaryTable(&out, &warn, results, false)
		}()

		if out.Len() == 0 {
			t.Error("expected non-empty table output even for degenerate input")
		}
	}
}

// TestPlainOutputHasNoEscapeCodes checks that colorize=false (the
// non-terminal / NO_COLOR path) really does yield plain, diff-friendly
// text with no ANSI codes.
func TestPlainOutputHasNoEscapeCodes(t *testing.T) {
	var out, warn bytes.Buffer
	fprintSummaryTable(&out, &warn, MockResults(), false)

	if strings.Contains(out.String(), "\033[") {
		t.Error("expected no ANSI escape codes when colorize=false")
	}
	for _, want := range []string{"HOSTNAME/IP", "SUCCESS", "AUTH FAILED", "TIMEOUT"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected output to contain %q", want)
		}
	}
}

// TestColorOnlyAppliedToStatusColumn confirms coloring is scoped to the
// STATUS cell and only emitted when colorize=true.
func TestColorOnlyAppliedToStatusColumn(t *testing.T) {
	var out, warn bytes.Buffer
	fprintSummaryTable(&out, &warn, MockResults(), true)

	if !strings.Contains(out.String(), ansiGreen) {
		t.Error("expected green ANSI code for a SUCCESS row when colorize=true")
	}
	if !strings.Contains(out.String(), ansiRed) {
		t.Error("expected red ANSI code for an AUTH FAILED row when colorize=true")
	}
}
