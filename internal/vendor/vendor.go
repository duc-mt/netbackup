// Package vendor defines how to retrieve a running configuration from each
// supported network OS. Adding a new platform means adding one entry to the
// Profiles map below -- nothing else in the codebase needs to change.
package vendor

import (
	"fmt"
	"strings"
)

// Profile describes how to pull a backup from one platform.
type Profile struct {
	// Key is the canonical identifier for the profile.
	Key string

	// Name is a human-readable label used in logs.
	Name string

	// Interactive selects the execution strategy:
	//   false -> open a plain non-interactive SSH "exec" channel and run
	//            BackupCommand once. This is the simplest, most reliable
	//            path and is what most network OS SSH daemons expect
	//            (Cisco IOS, Huawei VRP, Junos, Ruijie RGOS, FortiOS and
	//            Check Point Gaia clish all behave correctly this way --
	//            there is no tty attached, so CLI pagination does not
	//            kick in).
	//   true  -> allocate a pseudo-terminal, start an interactive shell,
	//            and feed SetupCommands + BackupCommand to it line by
	//            line. Needed for platforms whose SSH login drops into a
	//            restricted menu/shell rather than accepting a one-shot
	//            command (e.g. pfSense's console menu).
	Interactive bool

	// SetupCommands run before BackupCommand, only in Interactive mode
	// (e.g. disabling CLI paging).
	SetupCommands []string

	// BackupCommand is the command whose output is saved as the backup.
	BackupCommand string

	// FileExtension is used when naming the saved backup file.
	FileExtension string
}

// Profiles is the supported-platform registry. Keys are the lowercase
// "vendor" value used in the inventory file.
var Profiles = map[string]Profile{
	"cisco_ios": {
		Key:           "cisco_ios",
		Name:          "Cisco IOS / IOS-XE",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"huawei_vrp": {
		Key:           "huawei_vrp",
		Name:          "Huawei VRP",
		Interactive:   false,
		BackupCommand: "display current-configuration",
		FileExtension: ".cfg",
	},
	"juniper_junos": {
		Key:           "juniper_junos",
		Name:          "Juniper Junos",
		Interactive:   false,
		BackupCommand: "show configuration | display set",
		FileExtension: ".set",
	},
	"aruba": {
		Key:           "aruba",
		Name:          "Aruba AOS-S / AOS-CX",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"ruijie": {
		Key:           "ruijie",
		Name:          "Ruijie RGOS",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"fortigate": {
		Key:           "fortigate",
		Name:          "Fortinet FortiOS",
		Interactive:   false,
		BackupCommand: "show full-configuration",
		FileExtension: ".conf",
	},
	"checkpoint_gaia": {
		Key:           "checkpoint_gaia",
		Name:          "Check Point Gaia (clish)",
		Interactive:   false,
		BackupCommand: "show configuration",
		FileExtension: ".cfg",
	},
	// pfSense's default SSH login presents a restricted console menu
	// rather than accepting an arbitrary one-shot command, so this
	// platform needs an interactive shell. It also requires the SSH
	// account to have real shell access (not just the console menu)
	// configured in System > User Manager, or this will capture the
	// menu text instead of the XML config -- verify against your build
	// before relying on it.
	"pfsense": {
		Key:           "pfsense",
		Name:          "pfSense",
		Interactive:   true,
		SetupCommands: []string{""}, // wake the shell prompt
		BackupCommand: "cat /cf/conf/config.xml",
		FileExtension: ".xml",
	},
	"vyos": {
		Key:           "vyos",
		Name:          "VyOS",
		Interactive:   false,
		BackupCommand: "/opt/vyatta/bin/vyatta-op-cmd-wrapper show configuration commands",
		FileExtension: ".config",
	},
	"arista": {
		Key:           "arista",
		Name:          "Arista EOS",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	// Fallback for anything not explicitly mapped yet.
	"paloalto": {
		Key:           "paloalto",
		Name:          "Palo Alto PAN-OS",
		Interactive:   false,
		BackupCommand: "show config running",
		FileExtension: ".cfg",
	},
	"sophos": {
		Key:           "sophos",
		Name:          "Sophos",
		Interactive:   true,
		SetupCommands: []string{"4"},             // Select Device Console in menu
		BackupCommand: "show network interfaces", // Basic config dump fallback for XG
		FileExtension: ".cfg",
	},
	"generic": {
		Key:           "generic",
		Name:          "Generic / unspecified",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
}

var aliases = map[string]string{
	// Cisco
	"cisco":        "cisco_ios",
	"cisco_ios":    "cisco_ios",
	"cisco_ios_xe": "cisco_ios",
	"ios":          "cisco_ios",
	"ios_xe":       "cisco_ios",
	"iosxe":        "cisco_ios",

	// Juniper
	"junos":         "juniper_junos",
	"juniper":       "juniper_junos",
	"juniper_junos": "juniper_junos",

	// Huawei
	"huawei":     "huawei_vrp",
	"vrp":        "huawei_vrp",
	"huawei_vrp": "huawei_vrp",

	// Fortinet
	"fortinet":  "fortigate",
	"fortios":   "fortigate",
	"fortigate": "fortigate",

	// Arista
	"arista":     "arista",
	"eos":        "arista",
	"arista_eos": "arista",

	// Aruba
	"aruba":    "aruba",
	"aruba_cx": "aruba",
	"aos_cx":   "aruba",
	"aos_s":    "aruba",

	// Ruijie
	"ruijie": "ruijie",
	"rgos":   "ruijie",

	// Check Point
	"checkpoint":      "checkpoint_gaia",
	"checkpoint_gaia": "checkpoint_gaia",
	"gaia":            "checkpoint_gaia",

	// pfSense
	"pfsense": "pfsense",

	// Palo Alto
	"paloalto": "paloalto",
	"panos":    "paloalto",
	"pan_os":   "paloalto",

	// Sophos
	"sophos":    "sophos",
	"sophos_xg": "sophos",
	"sfos":      "sophos",

	// VyOS
	"vyos": "vyos",

	// Generic
	"generic": "generic",
}

// CanonicalKey normalizes a vendor name or alias (handling case, '-', ' ')
// and returns its canonical Profiles key if known.
func CanonicalKey(name string) (string, bool) {
	norm := strings.ToLower(strings.TrimSpace(name))
	norm = strings.NewReplacer("-", "_", " ", "_").Replace(norm)
	if canonical, ok := aliases[norm]; ok {
		return canonical, true
	}
	if _, ok := Profiles[norm]; ok {
		return norm, true
	}
	return "", false
}

// Get looks up a profile by vendor name or alias, case-insensitively and
// normalizing hyphens/spaces.
func Get(name string) (Profile, error) {
	canonical, ok := CanonicalKey(name)
	if !ok {
		return Profile{}, fmt.Errorf("unknown vendor %q (known: %v)", name, knownKeys())
	}
	p := Profiles[canonical]
	return p, nil
}

func knownKeys() []string {
	keys := make([]string, 0, len(Profiles))
	for k := range Profiles {
		keys = append(keys, k)
	}
	return keys
}
