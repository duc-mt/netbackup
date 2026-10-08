package sshclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func startMockSSHServer(t *testing.T) (int, func()) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
		KeyboardInteractiveCallback: func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if c.User() == "kbuser" {
				answers, err := client(c.User(), "", []string{"Password: "}, []bool{false})
				if err == nil && len(answers) == 1 && answers[0] == "kbpass" {
					return nil, nil
				}
			}
			return nil, fmt.Errorf("keyboard interactive rejected")
		},
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	done := make(chan struct{})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}

			go handleSSHConn(conn, serverConfig)
		}
	}()

	return port, func() {
		close(done)
		_ = listener.Close()
	}
}

func handleSSHConn(conn net.Conn, config *ssh.ServerConfig) {
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		conn.Close()
		return
	}
	defer sConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}

		go handleSession(channel, requests)
	}
}

func handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "pty-req":
			req.Reply(true, nil)
		case "shell":
			req.Reply(true, nil)
			// Read commands from stdin and respond
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := channel.Read(buf)
					if err != nil || n == 0 {
						return
					}
					input := string(buf[:n])
					if strings.Contains(input, "show") {
						_, _ = channel.Write([]byte("hostname router-sw-01\ninterface GigabitEthernet0/1\n"))
					} else {
						_, _ = channel.Write([]byte("ok\n"))
					}
				}
			}()
		case "exec":
			cmd := ""
			if len(req.Payload) >= 4 {
				cmd = string(req.Payload[4:])
			}
			req.Reply(true, nil)
			if strings.Contains(cmd, "sleep") {
				time.Sleep(500 * time.Millisecond)
			}
			if strings.Contains(cmd, "error") {
				_, _ = channel.Stderr().Write([]byte("syntax error in command"))
				_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
			} else {
				_, _ = channel.Write([]byte("! Configuration\nhostname core-switch-01\nend\n"))
				_, _ = channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			}
			return
		default:
			req.Reply(false, nil)
		}
	}
}

func TestConnectAndPasswordAuth(t *testing.T) {
	port, cleanup := startMockSSHServer(t)
	defer cleanup()

	opts := Options{
		ConnectTimeout:  2 * time.Second,
		CommandTimeout:  2 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// 1. Successful password authentication
	client, err := Connect(context.Background(), "127.0.0.1", port, "testuser", "testpass", opts)
	if err != nil {
		t.Fatalf("expected successful connect: %v", err)
	}
	defer client.Close()

	// 2. RunCommand test
	out, err := RunCommand(client, "show run", opts)
	if err != nil {
		t.Fatalf("expected successful command run: %v", err)
	}
	if !strings.Contains(out, "core-switch-01") {
		t.Errorf("unexpected command output: %q", out)
	}

	// 3. RunCommand error test
	_, err = RunCommand(client, "error cmd", opts)
	if err == nil {
		t.Fatalf("expected command error")
	}
	if !strings.Contains(err.Error(), "syntax error in command") {
		t.Errorf("expected stderr message in error, got: %v", err)
	}

	// 4. Failed authentication
	_, err = Connect(context.Background(), "127.0.0.1", port, "testuser", "wrongpass", opts)
	if err == nil {
		t.Fatalf("expected connection to fail with wrong password")
	}
}

func TestKeyboardInteractiveAuth(t *testing.T) {
	port, cleanup := startMockSSHServer(t)
	defer cleanup()

	opts := Options{
		ConnectTimeout:  2 * time.Second,
		CommandTimeout:  2 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Connect using keyboard-interactive credentials
	client, err := Connect(context.Background(), "127.0.0.1", port, "kbuser", "kbpass", opts)
	if err != nil {
		t.Fatalf("expected successful connect with keyboard-interactive: %v", err)
	}
	defer client.Close()
}

func TestRunCommandTimeout(t *testing.T) {
	port, cleanup := startMockSSHServer(t)
	defer cleanup()

	opts := Options{
		ConnectTimeout:  2 * time.Second,
		CommandTimeout:  100 * time.Millisecond,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := Connect(context.Background(), "127.0.0.1", port, "testuser", "testpass", opts)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	_, err = RunCommand(client, "sleep 5", opts)
	if err == nil {
		t.Fatalf("expected command timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunInteractive(t *testing.T) {
	port, cleanup := startMockSSHServer(t)
	defer cleanup()

	opts := Options{
		ConnectTimeout:  5 * time.Second,
		CommandTimeout:  10 * time.Second,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := Connect(context.Background(), "127.0.0.1", port, "testuser", "testpass", opts)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer client.Close()

	output, err := RunInteractive(client, []string{"terminal length 0", "show run"}, opts)
	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	if !strings.Contains(output, "router-sw-01") {
		t.Errorf("unexpected output from RunInteractive: %q", output)
	}
}

func TestKnownHostsCallbackStrict(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	pub := signer.PublicKey()

	f, _ := os.CreateTemp("", "known_hosts_strict")
	defer os.Remove(f.Name())
	f.Close()

	cb, err := KnownHostsCallback(f.Name(), PolicyStrict)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	err = cb("127.0.0.1", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}, pub)
	if err == nil {
		t.Fatal("expected strict policy to reject unknown key, got nil")
	}
}

func TestKnownHostsCallbackAcceptNew(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	pub := signer.PublicKey()

	f, _ := os.CreateTemp("", "known_hosts_acceptnew")
	defer os.Remove(f.Name())
	f.Close()

	cb, err := KnownHostsCallback(f.Name(), PolicyAcceptNew)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
	err = cb("127.0.0.1:22", addr, pub)
	if err != nil {
		t.Fatalf("expected accept-new policy to accept unknown key, got: %v", err)
	}

	content, _ := os.ReadFile(f.Name())
	if !strings.Contains(string(content), "127.0.0.1") {
		t.Fatalf("expected key to be written to known_hosts, file is: %s", string(content))
	}

	// second call should succeed via known key logic without re-appending
	err = cb("127.0.0.1:22", addr, pub)
	if err != nil {
		t.Fatalf("expected known key to be accepted: %v", err)
	}
}
