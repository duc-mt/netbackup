package vendor

import (
	"testing"
)

func TestGetVendor(t *testing.T) {
	p, err := Get("cisco_ios")
	if err != nil {
		t.Fatalf("unexpected error getting cisco_ios: %v", err)
	}
	if p.BackupCommand != "show running-config" {
		t.Errorf("expected 'show running-config', got %q", p.BackupCommand)
	}

	_, err = Get("non_existent_vendor")
	if err == nil {
		t.Error("expected error for unknown vendor, got nil")
	}
}

func TestKnownKeys(t *testing.T) {
	keys := knownKeys()
	if len(keys) < 5 {
		t.Errorf("expected at least 5 vendor profiles, got %d", len(keys))
	}
}
