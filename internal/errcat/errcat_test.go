package errcat

import (
	"errors"
	"testing"
)

type dummyTimeoutError struct{}

func (d dummyTimeoutError) Error() string   { return "i/o timeout" }
func (d dummyTimeoutError) Timeout() bool   { return true }
func (d dummyTimeoutError) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	tests := []struct {
		device   string
		stage    string
		err      error
		expected Category
	}{
		{"sw01", "connect", dummyTimeoutError{}, CategoryUnreachable},
		{"sw01", "command", dummyTimeoutError{}, CategoryTimeout},
		{"sw01", "command", errors.New("command timed out after 30s"), CategoryTimeout},
		{"sw01", "connect", errors.New("connection refused"), CategoryUnreachable},
		{"sw01", "connect", errors.New("ssh: handshake failed: permission denied"), CategoryAuthFailed},
		{"sw01", "command", errors.New("process exited with status 1"), CategoryCommandFailed},
		{"sw01", "connect", errors.New("ssh: unexpected packet type"), CategorySessionError},
		{"sw01", "write", errors.New("something totally random"), CategoryUnknown},
	}

	for _, tt := range tests {
		cerr := Classify(tt.device, tt.stage, tt.err)
		if cerr.Category != tt.expected {
			t.Errorf("Classify(%s, %s, %v): expected category %s, got %s", tt.device, tt.stage, tt.err, tt.expected, cerr.Category)
		}
	}
}
