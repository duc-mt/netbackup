// Package sshclient wraps golang.org/x/crypto/ssh with the two things a
// backup tool actually needs on top of it: hard timeouts at every stage
// (dial, handshake, command execution) and two execution strategies
// (plain one-shot exec, and interactive shell for platforms that need it).
package sshclient

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
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
}

// Connect opens a TCP connection and completes the SSH handshake, both
// bounded by opts.ConnectTimeout so an unreachable or black-holed device
// can never hang the run.
func Connect(address string, port int, username, password string, opts Options) (*ssh.Client, error) {
	addr := net.JoinHostPort(address, fmt.Sprintf("%d", port))

	conn, err := net.DialTimeout("tcp", addr, opts.ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		// Backup tooling against known internal infrastructure -- host
		// keys are not pinned here. For production hardening, replace
		// this with ssh.FixedHostKey(...) or a callback that checks a
		// known_hosts file you maintain for the air-gapped environment.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         opts.ConnectTimeout,
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
		session.Close() // best-effort; unblocks the goroutine above
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

		lastSize := output.Len()
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
			} else if time.Since(lastChange) >= settle && size > 0 {
				break // command output settled
			}
		}
	}

	return output.String(), nil
}
