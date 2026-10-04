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
)

type Credentials struct {
	Username string
	Password string
}

// Store caches configuration and credentials for the default profile and
// any per-site credential groups defined in .env or the process environment.
type Store struct {
	fileVals     map[string]string
	defaultCreds Credentials
}

// NewStore initializes a credential Store from the specified env file and
// environment variables, interactively prompting for default credentials
// if not provided.
func NewStore(envFile string) (*Store, error) {
	s := &Store{
		fileVals: map[string]string{},
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

	defaultCreds := Credentials{
		Username: firstNonEmpty(s.fileVals[EnvUsername], os.Getenv(EnvUsername)),
		Password: firstNonEmpty(s.fileVals[EnvPassword], os.Getenv(EnvPassword)),
	}

	if defaultCreds.Username == "" {
		u, err := promptLine("SSH username: ")
		if err != nil {
			return nil, fmt.Errorf("reading username: %w", err)
		}
		defaultCreds.Username = u
	}

	if defaultCreds.Password == "" {
		p, err := promptPassword("SSH password: ")
		if err != nil {
			return nil, fmt.Errorf("reading password: %w", err)
		}
		defaultCreds.Password = p
	}

	if defaultCreds.Username == "" || defaultCreds.Password == "" {
		return nil, fmt.Errorf("username and password are both required")
	}

	s.defaultCreds = defaultCreds
	return s, nil
}

// ForGroup returns credentials for the named group (case-insensitive).
// It searches for NETBACKUP_<GROUP>_USERNAME and NETBACKUP_<GROUP>_PASSWORD.
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

	u := firstNonEmpty(s.fileVals[userKey], os.Getenv(userKey))
	p := firstNonEmpty(s.fileVals[passKey], os.Getenv(passKey))

	if u == "" {
		u = s.defaultCreds.Username
	}
	if p == "" {
		p = s.defaultCreds.Password
	}

	return Credentials{Username: u, Password: p}
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

	vals := map[string]string{}
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
