// ==============================================================================
// Package main implements Implementation and logic for sshclient..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run sshclient.go [options]
// Notes:         Go package implementation
// ==============================================================================
// Package sshclient wraps golang.org/x/crypto/ssh with the two things a
// backup tool actually needs on top of it: hard timeouts at every stage
// (dial, handshake, command execution) and two execution strategies
// (plain one-shot exec, and interactive shell for platforms that need it).
package sshclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// Options controls the timeouts applied to every connection. All fields are
// required; see main.go for the CLI flags that populate this.
type Options struct {
	ConnectTimeout time.Duration // TCP dial + SSH handshake
	CommandTimeout time.Duration // time budget for running the backup command(s)

	// HostKeyCallback verifies the server's host key during the SSH
	// handshake. It is required: Connect returns an error if it is nil
	// rather than silently accepting any host key. Use LoadKnownHosts to
	// build one from a known_hosts file, ssh.FixedHostKey for a single
	// pinned key, or ssh.InsecureIgnoreHostKey() as an explicit, callsite-
	// visible opt-out (never as a hidden default).
	HostKeyCallback ssh.HostKeyCallback
}

// ErrNoHostKeyCallback is returned by Connect when opts.HostKeyCallback is
// nil, so callers can't accidentally run with host key checking disabled.
var ErrNoHostKeyCallback = errors.New("sshclient: Options.HostKeyCallback is required")

// KnownHostsPolicy defines how unknown host keys are handled.
type KnownHostsPolicy string

const (
	PolicyStrict    KnownHostsPolicy = "strict"     // reject unknown keys
	PolicyAcceptNew KnownHostsPolicy = "accept-new" // add unknown keys to the known_hosts file
)

// KnownHostsCallback builds a HostKeyCallback from an OpenSSH-format known_hosts
// file and enforces the given policy (strict or accept-new).
func KnownHostsCallback(path string, policy KnownHostsPolicy) (ssh.HostKeyCallback, error) {
	if policy != PolicyStrict && policy != PolicyAcceptNew {
		return nil, fmt.Errorf("invalid known_hosts policy: %q", policy)
	}

	// Create file if it doesn't exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		f, err := os.OpenFile(path, os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("creating known_hosts %q: %w", path, err)
		}
		f.Close()
	}

	checker, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("reading known_hosts %q: %w", path, err)
	}

	var mu sync.Mutex // protects concurrent writes to the known_hosts file

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := checker(hostname, remote, key)
		if err == nil {
			return nil // Key is known and matches
		}

		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
			// The key is completely unknown
			if policy == PolicyAcceptNew {
				mu.Lock()
				defer mu.Unlock()
				// We must re-check inside the lock to avoid races where another goroutine just added it
				checkerReloaded, err := knownhosts.New(path)
				if err == nil {
					if err := checkerReloaded(hostname, remote, key); err == nil {
						return nil
					}
				}

				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return fmt.Errorf("failed to open known_hosts to accept new key: %w", err)
				}
				defer f.Close()

				line := knownhosts.Line([]string{hostname}, key)
				if _, err := f.WriteString(line + "\n"); err != nil {
					return fmt.Errorf("failed to write new key to known_hosts: %w", err)
				}

				// Re-initialize checker so subsequent connections in this run use the updated file
				if c, err := knownhosts.New(path); err == nil {
					checker = c
				}
				return nil
			}
			return fmt.Errorf("host key verification failed: %s not found in known_hosts (policy=%s)", hostname, policy)
		}

		// Key mismatch (mitm or changed key)
		return fmt.Errorf("host key verification failed for %s: %w", hostname, err)
	}, nil
}

// Connect opens a TCP connection and completes the SSH handshake, both
// bounded by opts.ConnectTimeout so an unreachable or black-holed device
// can never hang the run. It also respects ctx: cancelling ctx (e.g. on
// SIGINT/SIGTERM) aborts an in-progress dial immediately.
func Connect(ctx context.Context, address string, port int, username, password string, opts Options) (*ssh.Client, error) {
	if opts.HostKeyCallback == nil {
		return nil, ErrNoHostKeyCallback
	}

	addr := net.JoinHostPort(address, fmt.Sprintf("%d", port))

	dialer := net.Dialer{Timeout: opts.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: opts.HostKeyCallback,
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA256,
			ssh.KeyAlgoRSASHA512,
			ssh.KeyAlgoRSA,
			ssh.KeyAlgoDSA,
		},
		Timeout: opts.ConnectTimeout,
		Config: ssh.Config{
			Ciphers: []string{
				"aes128-gcm@openssh.com",
				"aes256-gcm@openssh.com",
				"chacha20-poly1305@openssh.com",
				"aes128-ctr",
				"aes192-ctr",
				"aes256-ctr",
				"aes128-cbc",
				"3des-cbc",
			},
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group1-sha1",
			},
		},
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// RunCommand executes a single command on a plain (non-interactive, no PTY)
// exec channel -- the default, simplest, and most broadly compatible mode.
// It is bounded by opts.CommandTimeout: if the device never responds, the
// session is closed and a timeout error is returned instead of hanging.
func RunCommand(client *ssh.Client, command string, opts Options) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("opening session: %w", err)
	}
	defer session.Close()

	var stdout, stderr safeBuffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return stdout.String(), fmt.Errorf("command failed: %w (stderr: %s)", err, msg)
			}
			return stdout.String(), fmt.Errorf("command failed: %w", err)
		}
		return stdout.String(), nil

	case <-time.After(opts.CommandTimeout):
		_ = session.Signal(ssh.SIGKILL)
		session.Close()
		return "", fmt.Errorf("command timed out after %s", opts.CommandTimeout)
	}
}

// RunInteractive is for platforms whose SSH login does not accept a
// one-shot exec command (e.g. a restricted console menu). It allocates a
// PTY, starts a shell, feeds each command in sequence, and collects
// output until either the shell goes quiet for a short settle period or
// the hard opts.CommandTimeout is reached -- whichever comes first, so a
// device that never goes quiet still cannot hang the run indefinitely.
func RunInteractive(client *ssh.Client, commands []string, opts Options) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("opening session: %w", err)
	}
	defer session.Close()

	if err := session.RequestPty("xterm", 200, 800, ssh.TerminalModes{
		ssh.ECHO: 0,
	}); err != nil {
		return "", fmt.Errorf("requesting pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}

	var output safeBuffer
	session.Stdout = &output
	session.Stderr = &output

	if err := session.Shell(); err != nil {
		return "", fmt.Errorf("starting shell: %w", err)
	}

	deadline := time.Now().Add(opts.CommandTimeout)
	const settle = 1500 * time.Millisecond

	for _, cmd := range commands {
		if _, err := fmt.Fprintf(stdin, "%s\n", cmd); err != nil {
			return output.String(), fmt.Errorf("writing command to stdin: %w", err)
		}

		startLen := output.Len()
		lastSize := startLen
		lastChange := time.Now()

		for {
			if time.Now().After(deadline) {
				session.Close()
				return output.String(), fmt.Errorf("command timed out after %s", opts.CommandTimeout)
			}
			time.Sleep(150 * time.Millisecond)
			size := output.Len()
			if size != lastSize {
				lastSize = size
				lastChange = time.Now()
			} else if size > startLen && time.Since(lastChange) >= settle {
				break // command output arrived and settled
			} else if size == startLen && time.Since(lastChange) >= settle*2 {
				// command produced no new output within quiet window (e.g. empty command or prompt wake)
				break
			}
		}
	}

	return output.String(), nil
}
