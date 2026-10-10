// ==============================================================================
// Package main implements Implementation and logic for credentials_test..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run credentials_test.go [options]
// Notes:         Go package implementation
// ==============================================================================
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

func TestStoreForGroup(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := `
NETBACKUP_USERNAME=default_admin
NETBACKUP_PASSWORD=default_secret
NETBACKUP_SITE_A_USERNAME=site_a_user
NETBACKUP_SITE_A_PASSWORD=site_a_pass
NETBACKUP_PARTIAL_USERNAME=partial_user
`
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write temp env file: %v", err)
	}

	store, err := NewStore(envPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// 1. Default group
	cDef := store.ForGroup("")
	if cDef.Username != "default_admin" || cDef.Password != "default_secret" {
		t.Errorf("unexpected default creds: %+v", cDef)
	}

	// 2. Specific group site_a
	cSiteA := store.ForGroup("site_a")
	if cSiteA.Username != "site_a_user" || cSiteA.Password != "site_a_pass" {
		t.Errorf("unexpected site_a creds: %+v", cSiteA)
	}

	// 3. Partial group (password falls back to default)
	cPartial := store.ForGroup("partial")
	if cPartial.Username != "partial_user" || cPartial.Password != "default_secret" {
		t.Errorf("unexpected partial creds: %+v", cPartial)
	}

	// 4. Unknown group (fully falls back to default)
	cUnknown := store.ForGroup("nonexistent")
	if cUnknown.Username != "default_admin" || cUnknown.Password != "default_secret" {
		t.Errorf("unexpected unknown creds: %+v", cUnknown)
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
