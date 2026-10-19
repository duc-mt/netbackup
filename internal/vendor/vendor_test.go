package vendor

import (
	"reflect"
	"strings"
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

func TestValidateOutput(t *testing.T) {
	cisco, err := Get("cisco_ios")
	if err != nil {
		t.Fatalf("Get(cisco_ios): %v", err)
	}

	// 1. Empty output
	if err := cisco.ValidateOutput("   \n\t  "); err == nil {
		t.Errorf("expected error for empty output")
	}

	// 2. Complete output matching end marker
	validConfig := "hostname switch1\ninterface gig0/1\nend\n"
	if err := cisco.ValidateOutput(validConfig); err != nil {
		t.Errorf("unexpected error for valid config: %v", err)
	}

	// 3. Truncated output missing end marker
	truncatedConfig := "hostname switch1\ninterface gig0/1\n"
	if err := cisco.ValidateOutput(truncatedConfig); err == nil {
		t.Errorf("expected error for truncated config missing 'end'")
	}

	// 4. Vendor without end markers (e.g. generic)
	generic, err := Get("generic")
	if err != nil {
		t.Fatalf("Get(generic): %v", err)
	}
	if err := generic.ValidateOutput("some random text"); err != nil {
		t.Errorf("unexpected error for generic profile: %v", err)
	}
}

// newVendorAliases lists every name that must resolve to a canonical key for
// the Cisco Small Business, MikroTik, Cisco ASA and H3C/Comware support.
var newVendorAliases = map[string][]string{
	"cisco_sb": {
		"cisco_sb", "cisco_sf", "cisco_sg", "ciscosb", "ciscosf", "ciscosg",
		"cbs", "cbs350", "sg300", "sg350", "sg500", "sf300", "sf500",
	},
	"mikrotik":   {"mikrotik", "routeros", "ros"},
	"cisco_asa":  {"cisco_asa", "asa", "cisco_ftd", "ftd"},
	"huawei_vrp": {"h3c", "comware", "hp_comware"},
}

func TestNewVendorProfiles(t *testing.T) {
	want := map[string]Profile{
		"cisco_sb": {
			Key:           "cisco_sb",
			Name:          "Cisco Small Business (SF/SG/CBS)",
			Interactive:   false,
			SetupCommands: []string{"terminal datadump"},
			BackupCommand: "show running-config",
			EndMarkers:    []string{"\nend", "\r\nend", "\nexit", "\r\nexit"},
			FileExtension: ".cfg",
		},
		"mikrotik": {
			Key:           "mikrotik",
			Name:          "MikroTik RouterOS",
			Interactive:   false,
			BackupCommand: "/export verbose",
			FileExtension: ".rsc",
		},
		"cisco_asa": {
			Key:           "cisco_asa",
			Name:          "Cisco ASA",
			Interactive:   false,
			SetupCommands: []string{"terminal pager 0"},
			BackupCommand: "show running-config",
			EndMarkers:    []string{": end\n", ": end\r\n", "\nend", "\r\nend"},
			FileExtension: ".cfg",
		},
	}

	for key, w := range want {
		p, err := Get(key)
		if err != nil {
			t.Errorf("Get(%q): %v", key, err)
			continue
		}
		if !reflect.DeepEqual(p, w) {
			t.Errorf("Get(%q) = %+v, want %+v", key, p, w)
		}
		if reg, ok := Profiles[key]; !ok || reg.Key != key {
			t.Errorf("Profiles[%q] missing or its Key does not match: %+v", key, reg)
		}
	}
}

func TestCanonicalKeyNewVendors(t *testing.T) {
	for wantKey, names := range newVendorAliases {
		for _, name := range names {
			got, ok := CanonicalKey(name)
			if !ok || got != wantKey {
				t.Errorf("CanonicalKey(%q) = (%q, %v), want (%q, true)", name, got, ok, wantKey)
			}
		}
	}

	// Names that merely look similar must stay unknown.
	for _, name := range []string{"cisco_sbb", "cisco_asax", "mikrotik_ros", "sg-350x", ""} {
		if got, ok := CanonicalKey(name); ok {
			t.Errorf("CanonicalKey(%q) = (%q, true), want unknown", name, got)
		}
	}
}

func TestGetNewVendorAliases(t *testing.T) {
	for wantKey, names := range newVendorAliases {
		for _, name := range names {
			// Exercise the normalisation too: case, '-' and ' ' separators.
			variants := []string{
				name,
				strings.ToUpper(name),
				strings.ReplaceAll(name, "_", "-"),
				strings.ReplaceAll(name, "_", " "),
				"  " + name + "  ",
			}
			for _, v := range variants {
				p, err := Get(v)
				if err != nil {
					t.Errorf("Get(%q): %v", v, err)
					continue
				}
				if p.Key != wantKey {
					t.Errorf("Get(%q) resolved to %q, want %q", v, p.Key, wantKey)
				}
			}
		}
	}
}

func TestH3CAliasesShareHuaweiCommands(t *testing.T) {
	huawei, err := Get("huawei_vrp")
	if err != nil {
		t.Fatalf("Get(huawei_vrp): %v", err)
	}
	for _, name := range newVendorAliases["huawei_vrp"] {
		p, err := Get(name)
		if err != nil {
			t.Errorf("Get(%q): %v", name, err)
			continue
		}
		if !reflect.DeepEqual(p, huawei) {
			t.Errorf("Get(%q) = %+v, want the huawei_vrp profile %+v", name, p, huawei)
		}
		if p.BackupCommand != "display current-configuration" {
			t.Errorf("Get(%q).BackupCommand = %q, want %q", name, p.BackupCommand, "display current-configuration")
		}
	}
}

func TestAliasTargetsExist(t *testing.T) {
	for alias, target := range aliases {
		if _, ok := Profiles[target]; !ok {
			t.Errorf("alias %q points at unknown profile %q", alias, target)
		}
	}
}

func TestValidateOutputNewVendors(t *testing.T) {
	tests := []struct {
		name    string
		vendor  string
		output  string
		wantErr bool
	}{
		// Cisco Small Business: config blocks close with "exit"; "end" also accepted.
		{"cisco_sb empty", "cisco_sb", "  \n\t ", true},
		{"cisco_sb exit marker", "cisco_sb", "config-file-header\nsw1\nvlan database\nvlan 10\nexit\n", false},
		{"cisco_sb exit marker CRLF", "cisco_sb", "config-file-header\r\nsw1\r\ninterface gi1\r\nexit\r\n", false},
		{"cisco_sb end marker", "cisco_sb", "hostname sw1\ninterface gi1\nend\n", false},
		{"cisco_sb truncated", "cisco_sb", "config-file-header\nsw1\nv1.4.8.6 / R750_NIK_1_4_197_002\nCLI v1.0\nset system mode switch\n", true},

		// MikroTik: no end marker is defined, so only emptiness is rejected.
		{"mikrotik empty", "mikrotik", "\n\n", true},
		{"mikrotik export", "mikrotik", "# 2026-10-19 09:41:00 by RouterOS 7.16.1\n/ip address\nadd address=10.0.0.1/24 interface=ether1\n", false},
		{"mikrotik export without trailing newline", "mikrotik", "/system identity\nset name=rtr1", false},

		// Cisco ASA: the config ends with the ": end" comment line.
		{"cisco_asa empty", "cisco_asa", "", true},
		{"cisco_asa colon end", "cisco_asa", ": Saved\nASA Version 9.12(4)\nhostname asa1\n: end\n", false},
		{"cisco_asa colon end CRLF", "cisco_asa", ": Saved\r\nASA Version 9.12(4)\r\nhostname asa1\r\n: end\r\n", false},
		{"cisco_asa plain end", "cisco_asa", "hostname asa1\nend\n", false},
		{"cisco_asa truncated", "cisco_asa", ": Saved\nASA Version 9.12(4)\nhostname asa1\ninterface GigabitEthernet0/0\n", true},
		{"cisco_asa stuck pager", "cisco_asa", "ASA Version 9.12(4)\nhostname asa1\n<--- More --->\n", true},

		// H3C / Comware reuse the Huawei VRP "return" terminator.
		{"h3c return marker", "h3c", "sysname sw1\n#\nreturn\n", false},
		{"comware truncated", "comware", "sysname sw1\n#\ninterface Vlan-interface1\n", true},
	}
	for _, tt := range tests {
		p, err := Get(tt.vendor)
		if err != nil {
			t.Errorf("%s: Get(%q): %v", tt.name, tt.vendor, err)
			continue
		}
		err = p.ValidateOutput(tt.output)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: ValidateOutput(%q) error = %v, wantErr %v", tt.name, tt.output, err, tt.wantErr)
		}
	}
}
