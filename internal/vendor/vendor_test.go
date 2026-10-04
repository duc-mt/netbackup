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

	pVyOS, err := Get("vyos")
	if err != nil {
		t.Fatalf("unexpected error getting vyos: %v", err)
	}
	if pVyOS.BackupCommand != "/opt/vyatta/bin/vyatta-op-cmd-wrapper show configuration commands" {
		t.Errorf("expected '/opt/vyatta/bin/vyatta-op-cmd-wrapper show configuration commands', got %q", pVyOS.BackupCommand)
	}

	pArista, err := Get("arista")
	if err != nil {
		t.Fatalf("unexpected error getting arista: %v", err)
	}
	if pArista.Key != "arista" || pArista.BackupCommand != "show running-config" {
		t.Errorf("unexpected arista profile: %+v", pArista)
	}

	_, err = Get("non_existent_vendor")
	if err == nil {
		t.Error("expected error for unknown vendor, got nil")
	}
}

func TestAliases(t *testing.T) {
	cases := map[string]string{
		"cisco":      "cisco_ios",
		"cisco-ios":  "cisco_ios",
		"IOS-XE":     "cisco_ios",
		"junos":      "juniper_junos",
		"juniper":    "juniper_junos",
		"huawei":     "huawei_vrp",
		"vrp":        "huawei_vrp",
		"fortinet":   "fortigate",
		"fortios":    "fortigate",
		"eos":        "arista",
		"checkpoint": "checkpoint_gaia",
		"aruba-cx":   "aruba",
		"rgos":       "ruijie",
	}

	for input, expectedKey := range cases {
		p, err := Get(input)
		if err != nil {
			t.Errorf("failed to resolve alias %q: %v", input, err)
			continue
		}
		if p.Key != expectedKey {
			t.Errorf("input %q resolved to key %q, want %q", input, p.Key, expectedKey)
		}
	}
}

func TestKnownKeys(t *testing.T) {
	keys := knownKeys()
	if len(keys) < 6 {
		t.Errorf("expected at least 6 vendor profiles, got %d", len(keys))
	}
}
