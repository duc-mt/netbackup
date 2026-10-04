package backup

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
	"time"

	"netbackup/internal/inventory"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"core-sw-01", "core-sw-01"},
		{"../../etc/passwd", "______etc_passwd"},
		{"sw:01!@#", "sw_01___"},
		{"10.10.1.1", "10_10_1_1"},
	}

	for _, tt := range tests {
		if got := sanitize(tt.input); got != tt.expected {
			t.Errorf("sanitize(%q): expected %q, got %q", tt.input, tt.expected, got)
		}
	}
}

func TestFindLatestBackupAndPrune(t *testing.T) {
	dir := t.TempDir()
	dev := inventory.Device{
		Hostname: "test-sw-01",
		Vendor:   "cisco_ios",
	}
	ext := ".cfg"

	// Create 3 simulated backup files across 3 runs
	run1 := filepath.Join(dir, "backup-20260101-1000", dev.Vendor)
	run2 := filepath.Join(dir, "backup-20260102-1000", dev.Vendor)
	run3 := filepath.Join(dir, "backup-20260103-1000", dev.Vendor)

	_ = os.MkdirAll(run1, 0o750)
	_ = os.MkdirAll(run2, 0o750)
	_ = os.MkdirAll(run3, 0o750)

	file1 := filepath.Join(run1, dev.Hostname+ext)
	file2 := filepath.Join(run2, dev.Hostname+ext)
	file3 := filepath.Join(run3, dev.Hostname+ext)

	content1 := "config version 1"
	content2 := "config version 2"
	content3 := "config version 3"

	_ = os.WriteFile(file1, []byte(content1), 0o640)
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(file2, []byte(content2), 0o640)
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(file3, []byte(content3), 0o640)

	latestPath, latestHash, found := findLatestBackup(dir, dev, ext)
	if !found {
		t.Fatalf("expected to find latest backup")
	}
	if latestPath != file3 {
		t.Errorf("expected latest path to be %s, got %s", file3, latestPath)
	}
	if latestHash != sha256.Sum256([]byte(content3)) {
		t.Errorf("hash mismatch for latest content")
	}

	// Test prune: keep only 2 backups
	pruned := pruneBackups(dir, dev, ext, 2, 0)
	if pruned != 1 {
		t.Errorf("expected 1 file to be pruned, got %d", pruned)
	}

	// file1 should be deleted, file2 and file3 should remain
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Errorf("expected file1 to be deleted")
	}
	if _, err := os.Stat(file2); err != nil {
		t.Errorf("expected file2 to exist")
	}
	if _, err := os.Stat(file3); err != nil {
		t.Errorf("expected file3 to exist")
	}

	// Test cross-compatibility: dev with alias "cisco" should find the latest backup stored under "cisco_ios"
	aliasDev := inventory.Device{
		Hostname: "test-sw-01",
		Vendor:   "cisco",
	}
	aliasLatestPath, _, aliasFound := findLatestBackup(dir, aliasDev, ext)
	if !aliasFound {
		t.Fatalf("expected to find latest backup using vendor alias 'cisco'")
	}
	if aliasLatestPath != file3 {
		t.Errorf("expected latest path using alias to be %s, got %s", file3, aliasLatestPath)
	}
}
