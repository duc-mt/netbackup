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

// Resolve loads credentials using the priority described above. envFile may
// be empty, in which case step 1 is skipped.
func Resolve(envFile string) (Credentials, error) {
	fileVals := map[string]string{}
	if envFile != "" {
		vals, err := parseEnvFile(envFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Credentials{}, fmt.Errorf("reading env file %q: %w", envFile, err)
		}
		if err == nil {
			fileVals = vals
		}
		// A missing --env-file is not an error: the caller may be relying
		// on real environment variables or the interactive prompt instead.
	}

	creds := Credentials{
		Username: firstNonEmpty(fileVals[EnvUsername], os.Getenv(EnvUsername)),
		Password: firstNonEmpty(fileVals[EnvPassword], os.Getenv(EnvPassword)),
	}

	if creds.Username == "" {
		u, err := promptLine("SSH username: ")
		if err != nil {
			return Credentials{}, fmt.Errorf("reading username: %w", err)
		}
		creds.Username = u
	}

	if creds.Password == "" {
		p, err := promptPassword("SSH password: ")
		if err != nil {
			return Credentials{}, fmt.Errorf("reading password: %w", err)
		}
		creds.Password = p
	}

	if creds.Username == "" || creds.Password == "" {
		return Credentials{}, fmt.Errorf("username and password are both required")
	}

	return creds, nil
}

// parseEnvFile reads simple KEY=VALUE lines. No external dependency is used
// so the build has nothing extra to vendor for this.
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
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
	return vals, scanner.Err()
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
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	bytePw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
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
