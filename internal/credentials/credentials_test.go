package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `
# Comment line
NETBACKUP_USERNAME=admin
NETBACKUP_PASSWORD="secret_password"
QUOTED_SINGLE='single_quoted'
WITH_EQUALS=foo=bar
PASSTHROUGH_QUOTE=p@ss"word!
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}

	vals, err := parseEnvFile(envPath)
	if err != nil {
		t.Fatalf("parseEnvFile returned unexpected error: %v", err)
	}

	tests := map[string]string{
		"NETBACKUP_USERNAME": "admin",
		"NETBACKUP_PASSWORD": "secret_password",
		"QUOTED_SINGLE":      "single_quoted",
		"WITH_EQUALS":        "foo=bar",
		"PASSTHROUGH_QUOTE":  `p@ss"word!`,
	}

	for k, expected := range tests {
		if got := vals[k]; got != expected {
			t.Errorf("key %q: expected %q, got %q", k, expected, got)
		}
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`"hello`, `"hello`},
		{`hello"`, `hello"`},
		{`p@ss"word!`, `p@ss"word!`},
		{`  "spaces"  `, "spaces"},
	}

	for _, tt := range tests {
		if got := unquote(tt.input); got != tt.expected {
			t.Errorf("unquote(%q): expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "first", "second"); got != "first" {
		t.Errorf("expected 'first', got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
