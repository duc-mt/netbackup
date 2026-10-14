// ==============================================================================
// Package main implements Implementation and logic for credentials..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run credentials.go [options]
// Notes:         Go package implementation
// ==============================================================================
// Package credentials resolves the SSH username/password the tool uses,
// in this strict priority order, and never from source code:
//
//  1. An .env file (KEY=VALUE, simple and dependency-free to parse)
//  2. Process environment variables
//  3. An interactive, non-echoing terminal prompt
//
// Whichever source is used, the values never get logged or written to the
// output/log files.
package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	EnvUsername = "NETBACKUP_USERNAME"
	EnvPassword = "NETBACKUP_PASSWORD"
	EnvKeyFile  = "NETBACKUP_KEY_FILE"

	// Global jump / bastion host defaults.  Per-named-jump credentials use
	// NETBACKUP_JUMP_<NAME>_USERNAME / _PASSWORD / _KEY_FILE (same group-key pattern as
	// device credentials).
	EnvJumpHost     = "NETBACKUP_JUMP_HOST" // host[:port], e.g. bastion.example.com:22
	EnvJumpUsername = "NETBACKUP_JUMP_USERNAME"
	EnvJumpPassword = "NETBACKUP_JUMP_PASSWORD"
	EnvJumpKeyFile  = "NETBACKUP_JUMP_KEY_FILE"
)

// JumpCredentials holds connection details for a bastion / jump host.
type JumpCredentials struct {
	Host     string // host[:port]; empty disables jump
	Username string
	Password string
	KeyFile  string
}

type Credentials struct {
	Username string
	Password string
	KeyFile  string
}

// Store caches configuration and credentials for the default profile and
// any per-site credential groups defined in .env or the process environment.
type Store struct {
	fileVals     map[string]string
	defaultCreds Credentials
	jumpDefaults JumpCredentials // global jump host defaults (from env / .env)
}

// NewStore initializes a credential Store from the specified env file and
// environment variables, interactively prompting for default credentials
// if not provided.
func NewStore(envFile string) (*Store, error) {
	s := &Store{
		fileVals: make(map[string]string),
	}
	if envFile != "" {
		vals, err := parseEnvFile(envFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("reading env file %q: %w", envFile, err)
		}
		if err == nil {
			s.fileVals = vals
		}
	}

	// Seed global jump host defaults (no interactive prompt -- bastions are
	// always configured via env/file rather than prompted interactively).
	s.jumpDefaults = JumpCredentials{
		Host:     firstNonEmpty(s.fileVals[EnvJumpHost], os.Getenv(EnvJumpHost)),
		Username: firstNonEmpty(s.fileVals[EnvJumpUsername], os.Getenv(EnvJumpUsername)),
		Password: firstNonEmpty(s.fileVals[EnvJumpPassword], os.Getenv(EnvJumpPassword)),
		KeyFile:  firstNonEmpty(s.fileVals[EnvJumpKeyFile], os.Getenv(EnvJumpKeyFile)),
	}

	defaultCreds := Credentials{
		Username: firstNonEmpty(s.fileVals[EnvUsername], os.Getenv(EnvUsername)),
		Password: firstNonEmpty(s.fileVals[EnvPassword], os.Getenv(EnvPassword)),
		KeyFile:  firstNonEmpty(s.fileVals[EnvKeyFile], os.Getenv(EnvKeyFile)),
	}

	if defaultCreds.Username == "" {
		u, err := promptLine("SSH username: ")
		if err != nil {
			return nil, fmt.Errorf("reading username: %w", err)
		}
		defaultCreds.Username = u
	}

	if defaultCreds.Password == "" && defaultCreds.KeyFile == "" {
		p, err := promptPassword("SSH password (or set NETBACKUP_KEY_FILE): ")
		if err != nil {
			return nil, fmt.Errorf("reading password: %w", err)
		}
		defaultCreds.Password = p
	}

	if defaultCreds.Username == "" || (defaultCreds.Password == "" && defaultCreds.KeyFile == "") {
		return nil, fmt.Errorf("username and either password or key file are required")
	}

	s.defaultCreds = defaultCreds
	return s, nil
}

// ForGroup returns credentials for the named group (case-insensitive).
// It searches for NETBACKUP_<GROUP>_USERNAME, NETBACKUP_<GROUP>_PASSWORD, and NETBACKUP_<GROUP>_KEY_FILE.
// Any missing field falls back to the default credentials.
func (s *Store) ForGroup(group string) Credentials {
	group = strings.ToUpper(strings.TrimSpace(group))
	if group == "" || group == "DEFAULT" {
		return s.defaultCreds
	}

	cleanGroup := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, group)

	userKey := fmt.Sprintf("NETBACKUP_%s_USERNAME", cleanGroup)
	passKey := fmt.Sprintf("NETBACKUP_%s_PASSWORD", cleanGroup)
	fileKey := fmt.Sprintf("NETBACKUP_%s_KEY_FILE", cleanGroup)

	u := firstNonEmpty(s.fileVals[userKey], os.Getenv(userKey))
	p := firstNonEmpty(s.fileVals[passKey], os.Getenv(passKey))
	k := firstNonEmpty(s.fileVals[fileKey], os.Getenv(fileKey))

	if u == "" {
		u = s.defaultCreds.Username
	}
	if p == "" && k == "" {
		p = s.defaultCreds.Password
		k = s.defaultCreds.KeyFile
	}

	return Credentials{Username: u, Password: p, KeyFile: k}
}

// JumpDefaults returns the global bastion / jump host connection details
// loaded from NETBACKUP_JUMP_HOST / NETBACKUP_JUMP_USERNAME / NETBACKUP_JUMP_PASSWORD
// (or their .env equivalents). Host will be empty if none were configured.
func (s *Store) JumpDefaults() JumpCredentials {
	return s.jumpDefaults
}

// ForJumpHost resolves credentials for a named jump host (the value from the
// inventory's jump_host column). It uses the following priority:
//
//  1. NETBACKUP_JUMP_<NAME>_USERNAME / NETBACKUP_JUMP_<NAME>_PASSWORD / NETBACKUP_JUMP_<NAME>_KEY_FILE
//  2. Global NETBACKUP_JUMP_USERNAME / NETBACKUP_JUMP_PASSWORD / NETBACKUP_JUMP_KEY_FILE
//  3. The device default credentials (username and key file, as a last resort)
//
// name is case-insensitive and sanitised before key lookup.
func (s *Store) ForJumpHost(name string) JumpCredentials {
	name = strings.ToUpper(strings.TrimSpace(name))

	cleanName := strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, name)

	userKey := fmt.Sprintf("NETBACKUP_JUMP_%s_USERNAME", cleanName)
	passKey := fmt.Sprintf("NETBACKUP_JUMP_%s_PASSWORD", cleanName)
	hostKey := fmt.Sprintf("NETBACKUP_JUMP_%s_HOST", cleanName)
	keyFileKey := fmt.Sprintf("NETBACKUP_JUMP_%s_KEY_FILE", cleanName)

	host := firstNonEmpty(s.fileVals[hostKey], os.Getenv(hostKey), s.jumpDefaults.Host)
	u := firstNonEmpty(s.fileVals[userKey], os.Getenv(userKey), s.jumpDefaults.Username, s.defaultCreds.Username)
	p := firstNonEmpty(s.fileVals[passKey], os.Getenv(passKey), s.jumpDefaults.Password)
	k := firstNonEmpty(s.fileVals[keyFileKey], os.Getenv(keyFileKey), s.jumpDefaults.KeyFile, s.defaultCreds.KeyFile)

	return JumpCredentials{Host: host, Username: u, Password: p, KeyFile: k}
}

// Resolve loads default credentials using the priority described above.
func Resolve(envFile string) (Credentials, error) {
	s, err := NewStore(envFile)
	if err != nil {
		return Credentials{}, fmt.Errorf("resolving credentials: %w", err)
	}
	return s.defaultCreds, nil
}

// parseEnvFile reads simple KEY=VALUE lines. No external dependency is used
// so the build has nothing extra to vendor for this.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening env file %q: %w", path, err)
	}
	defer f.Close()

	vals := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // tolerate stray lines rather than aborting the whole run
		}
		key := strings.TrimSpace(parts[0])
		val := unquote(parts[1])
		vals[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning env file %q: %w", path, err)
	}
	return vals, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) ||
			(strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input from terminal: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password from terminal: %w", err)
	}
	return string(bytePw), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
