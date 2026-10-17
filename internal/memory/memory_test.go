package memory

import (
	"path/filepath"
	"testing"
)

func TestStoreOperations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.json")

	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load empty failed: %v", err)
	}

	if len(s.List()) != 0 {
		t.Fatalf("expected 0 routes, got %d", len(s.List()))
	}

	err = s.Record("router1", "192.0.2.1", "jump1", "192.0.2.254:22", "auto-fallback")
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	r, ok := s.Get("192.0.2.1")
	if !ok || r.JumpEndpoint != "192.0.2.254:22" {
		t.Fatalf("expected jump1 endpoint, got %+v", r)
	}

	r2, ok := s.Get("router1")
	if !ok || r2.JumpHost != "jump1" {
		t.Fatalf("expected jump1 host, got %+v", r2)
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if len(s2.List()) != 1 {
		t.Fatalf("expected 1 route, got %d", len(s2.List()))
	}

	_ = s.Record("router1", "192.0.2.1", "jump1", "192.0.2.254:22", "auto-fallback")
	if s.routes["192.0.2.1"].SuccessCount != 2 {
		t.Fatalf("expected SuccessCount 2, got %d", s.routes["192.0.2.1"].SuccessCount)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("expected 0 routes after clear, got %d", len(s.List()))
	}
}
