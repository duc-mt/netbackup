// Package vendor defines how to retrieve a running configuration from each
// supported network OS. Adding a new platform means adding one entry to the
// Profiles map below -- nothing else in the codebase needs to change.
package vendor

import "fmt"

// Profile describes how to pull a backup from one platform.
type Profile struct {
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
		Name:          "Cisco IOS / IOS-XE",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"huawei_vrp": {
		Name:          "Huawei VRP",
		Interactive:   false,
		BackupCommand: "display current-configuration",
		FileExtension: ".cfg",
	},
	"juniper_junos": {
		Name:          "Juniper Junos",
		Interactive:   false,
		BackupCommand: "show configuration | display set",
		FileExtension: ".set",
	},
	"aruba": {
		Name:          "Aruba AOS-S / AOS-CX",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"ruijie": {
		Name:          "Ruijie RGOS",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
	"fortigate": {
		Name:          "Fortinet FortiOS",
		Interactive:   false,
		BackupCommand: "show full-configuration",
		FileExtension: ".conf",
	},
	"checkpoint_gaia": {
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
		Name:          "pfSense",
		Interactive:   true,
		SetupCommands: []string{""}, // wake the shell prompt
		BackupCommand: "cat /cf/conf/config.xml",
		FileExtension: ".xml",
	},
	// Fallback for anything not explicitly mapped yet.
	"generic": {
		Name:          "Generic / unspecified",
		Interactive:   false,
		BackupCommand: "show running-config",
		FileExtension: ".cfg",
	},
}

// Get looks up a profile by key, case-sensitively matching the inventory
// file's vendor column (callers normalise case before calling this).
func Get(key string) (Profile, error) {
	p, ok := Profiles[key]
	if !ok {
		return Profile{}, fmt.Errorf("unknown vendor key %q (known: %v)", key, knownKeys())
	}
	return p, nil
}

func knownKeys() []string {
	keys := make([]string, 0, len(Profiles))
	for k := range Profiles {
		keys = append(keys, k)
	}
	return keys
}
