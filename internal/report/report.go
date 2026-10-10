// Package report renders a colorized "Result Table" summary of a backup
// run to the terminal. It is independent of the structured run log the
// logging package writes to disk -- that log is for grepping and
// machine parsing, this table is for a human glancing at a terminal.
//
// Like the rest of netbackup, this package adds no third-party
// dependency: it reuses golang.org/x/term (already vendored for SSH host
// key handling) only to detect whether stdout is a real terminal, and
// draws the table itself with box-drawing characters. That keeps the
// project's "vendor everything, build with no internet access" promise
// intact instead of pulling in a table-rendering library just for this.
//
// Nothing here panics. A result that can't be formatted (e.g. a
// corrupt negative duration) degrades to a placeholder cell and a
// warning on stderr instead of aborting the table, matching the rest of
// netbackup's "one bad entry never stops the run" philosophy. The
// top-level PrintSummaryTable additionally recovers from anything
// unforeseen in rendering itself, so a bug here can never crash the
// main goroutine after a backup run has already done its work.
package report

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// BackupResult is one row of the result table: the outcome of backing up
// a single device, in the shape a renderer needs. It is intentionally
// decoupled from backup.Result so this package stays usable on its own
// (e.g. for a dry-run report, or replaying a saved run) -- see
// FromResults in adapter.go for converting a real run's output into this
// shape.
type BackupResult struct {
	HostnameIP string  // hostname and/or address, however the caller wants it labeled
	Platform   string  // vendor/platform key, e.g. "cisco_ios", "juniper_junos"
	Status     string  // free-form, e.g. "SUCCESS", "AUTH FAILED", "TIMEOUT" -- matched case-insensitively for color
	FileSize   int64   // bytes written; use -1 when no size applies (e.g. a failed or skipped-unchanged backup)
	Duration   float64 // elapsed time in seconds
	Error      error   // nil on success
}

// Well-known status labels. Callers aren't required to use these exact
// strings -- colorForStatus matches case-insensitively by substring so
// any reasonably-named status still gets colored sensibly -- but using
// them keeps output consistent across the codebase.
const (
	StatusSuccess    = "SUCCESS"
	StatusUnchanged  = "UNCHANGED"
	StatusAuthFailed = "AUTH FAILED"
	StatusTimeout    = "TIMEOUT"
	StatusFailed     = "FAILED"
)

// ANSI color codes. Kept minimal on purpose: just enough to distinguish
// success from the two failure classes the spec calls out.
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
)

// tableColumns are the headers, in display order. Row cells in tableRow
// must line up with this slice index for index.
var tableColumns = [6]string{"HOSTNAME/IP", "PLATFORM", "STATUS", "FILE SIZE", "DURATION", "ERROR"}

// maxErrorColumnWidth caps how wide the ERROR column can grow, so one
// long SSH error message can't blow out the whole table's layout.
const maxErrorColumnWidth = 48

// FormatFileSize renders a byte count as a human-readable size
// (B/KB/MB/...). It returns an error instead of panicking on a size that
// can never be valid, so a single bad value degrades only its own row.
// FileSize == -1 means "not applicable" and renders as "-" with no error.
func FormatFileSize(bytes int64) (string, error) {
	if bytes == -1 {
		return "-", nil
	}
	if bytes < 0 {
		return "", fmt.Errorf("invalid file size: %d bytes", bytes)
	}

	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes), nil
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp]), nil
}

// FormatDuration renders an elapsed time in seconds as a short, fixed
// string. It returns an error instead of panicking on a negative value,
// which can only indicate a bug upstream (e.g. a bad time subtraction).
func FormatDuration(seconds float64) (string, error) {
	if seconds < 0 {
		return "", fmt.Errorf("invalid duration: %.3f seconds", seconds)
	}
	if seconds < 60 {
		return fmt.Sprintf("%.2fs", seconds), nil
	}
	minutes := int(seconds) / 60
	rem := seconds - float64(minutes*60)
	return fmt.Sprintf("%dm%04.1fs", minutes, rem), nil
}

// formatError renders the Error field for display: "-" when nil, and the
// message truncated to maxErrorColumnWidth otherwise.
func formatError(err error) string {
	if err == nil {
		return "-"
	}
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if utf8.RuneCountInString(msg) <= maxErrorColumnWidth {
		return msg
	}
	runes := []rune(msg)
	return string(runes[:maxErrorColumnWidth-1]) + "…"
}

// colorForStatus maps a status string to an ANSI color code: SUCCESS /
// UNCHANGED-like statuses are green, AUTH FAILED and other failures are
// red, TIMEOUT is yellow. Matching is case-insensitive and by substring
// so callers aren't tied to the exact Status* constants. An unrecognized
// status renders uncolored rather than defaulting to an alarming color.
func colorForStatus(status string) string {
	s := strings.ToUpper(status)
	switch {
	case strings.Contains(s, "SUCCESS"), strings.Contains(s, "UNCHANGED"), strings.Contains(s, "SAVED"), strings.Contains(s, "OK"):
		return ansiGreen
	case strings.Contains(s, "TIMEOUT"):
		return ansiYellow
	case strings.Contains(s, "AUTH"), strings.Contains(s, "FAIL"), strings.Contains(s, "ERROR"):
		return ansiRed
	default:
		return ""
	}
}

// tableRow holds one row's plain-text cells (no ANSI codes -- used for
// column-width math) plus the color to apply to the STATUS cell.
type tableRow struct {
	cells       [6]string
	statusColor string
}

// buildRow formats one BackupResult into display cells. It never panics:
// a formatting failure is recorded as a warning and the cell falls back
// to "N/A", so one malformed result can't stop the rest of the table
// from rendering.
func buildRow(r BackupResult) (tableRow, []string) {
	var warnings []string
	row := tableRow{statusColor: colorForStatus(r.Status)}

	row.cells[0] = orDash(r.HostnameIP)
	row.cells[1] = orDash(r.Platform)
	row.cells[2] = orDash(r.Status)

	size, err := FormatFileSize(r.FileSize)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s: file size: %v", orDash(r.HostnameIP), err))
		size = "N/A"
	}
	row.cells[3] = size

	dur, err := FormatDuration(r.Duration)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s: duration: %v", orDash(r.HostnameIP), err))
		dur = "N/A"
	}
	row.cells[4] = dur

	row.cells[5] = formatError(r.Error)

	return row, warnings
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// PrintSummaryTable renders results as a colorized table on stdout.
// Color is used only when stdout is a real terminal and NO_COLOR isn't
// set, so piping the output to a file or another program yields plain,
// diff-friendly text.
//
// This function never panics: per-row formatting problems are reported
// to stderr as warnings (see buildRow) and a recover() guards against
// anything unforeseen in the rendering itself, printing a one-line
// warning instead of taking down the caller's main goroutine.
func PrintSummaryTable(results []BackupResult) {
	colorize := os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd()))
	fprintSummaryTable(os.Stdout, os.Stderr, results, colorize)
}

// fprintSummaryTable does the real work against explicit writers so it
// can be exercised by tests without touching the process's real
// stdout/stderr.
func fprintSummaryTable(out, warn io.Writer, results []BackupResult, colorize bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(warn, "report: recovered while rendering result table: %v\n", r)
		}
	}()

	rows := make([]tableRow, 0, len(results))
	for _, r := range results {
		row, warnings := buildRow(r)
		rows = append(rows, row)
		for _, w := range warnings {
			fmt.Fprintf(warn, "report: %s\n", w)
		}
	}

	widths := columnWidths(rows)
	writeBorder(out, widths, "┌", "┬", "┐")
	writeHeader(out, widths)
	writeBorder(out, widths, "├", "┼", "┤")
	for _, row := range rows {
		writeRow(out, widths, row, colorize)
	}
	writeBorder(out, widths, "└", "┴", "┘")
}

func columnWidths(rows []tableRow) [6]int {
	var widths [6]int
	for i, h := range tableColumns {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row.cells {
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}
	return widths
}

func writeBorder(w io.Writer, widths [6]int, left, mid, right string) {
	fmt.Fprint(w, left)
	for i, width := range widths {
		fmt.Fprint(w, strings.Repeat("─", width+2))
		if i < len(widths)-1 {
			fmt.Fprint(w, mid)
		}
	}
	fmt.Fprintln(w, right)
}

func writeHeader(w io.Writer, widths [6]int) {
	fmt.Fprint(w, "│")
	for i, h := range tableColumns {
		fmt.Fprintf(w, " %s │", padRight(h, widths[i]))
	}
	fmt.Fprintln(w)
}

func writeRow(w io.Writer, widths [6]int, row tableRow, colorize bool) {
	fmt.Fprint(w, "│")
	for i, cell := range row.cells {
		text := padRight(cell, widths[i])
		if colorize && i == 2 && row.statusColor != "" {
			text = row.statusColor + text + ansiReset
		}
		fmt.Fprintf(w, " %s │", text)
	}
	fmt.Fprintln(w)
}

// padRight pads s with spaces up to width, counting runes rather than
// bytes so multi-byte hostnames/platform names still line up.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// MockResults returns illustrative sample data covering success,
// unchanged, auth-failure and timeout cases, for demos and tests. See
// cmd/resultdemo for a runnable example.
func MockResults() []BackupResult {
	return []BackupResult{
		{
			HostnameIP: "core-sw01 (10.0.1.1)",
			Platform:   "cisco_ios",
			Status:     StatusSuccess,
			FileSize:   45896,
			Duration:   3.42,
		},
		{
			HostnameIP: "edge-rtr02 (10.0.2.5)",
			Platform:   "juniper_junos",
			Status:     StatusUnchanged,
			FileSize:   -1,
			Duration:   2.10,
		},
		{
			HostnameIP: "fw-dc01 (10.0.3.10)",
			Platform:   "fortinet_fortios",
			Status:     StatusAuthFailed,
			FileSize:   -1,
			Duration:   1.05,
			Error:      fmt.Errorf("ssh: unable to authenticate, attempted methods [password], no supported methods remain"),
		},
		{
			HostnameIP: "sw-branch09 (10.0.9.2)",
			Platform:   "arista_eos",
			Status:     StatusTimeout,
			FileSize:   -1,
			Duration:   30.00,
			Error:      fmt.Errorf("command timed out after 30s"),
		},
		{
			HostnameIP: "sw-branch14 (10.0.14.3)",
			Platform:   "cisco_ios",
			Status:     StatusFailed,
			FileSize:   -1,
			Duration:   0.87,
			Error:      fmt.Errorf("no route to host"),
		},
	}
}
