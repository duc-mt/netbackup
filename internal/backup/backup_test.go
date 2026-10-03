package backup

import (
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"core-sw-01", "core-sw-01"},
		{"../../etc/passwd", ".._.._etc_passwd"},
		{"sw:01!@#", "sw_01___"},
		{"10.10.1.1", "10_10_1_1"},
	}

	for _, tt := range tests {
		if got := sanitize(tt.input); got != tt.expected {
			t.Errorf("sanitize(%q): expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}
