// Command netbackup backs up running configurations from a list of network
// devices over SSH. It is built to run as a single static binary with no
// runtime dependencies, for use on air-gapped management networks.
//
// Usage:
//
//	netbackup -inventory inventory.csv -out backups -env-file .env
//
// See README.md for the full option list and build instructions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"netbackup/internal/backup"
	"netbackup/internal/credentials"
	"netbackup/internal/inventory"
	"netbackup/internal/logging"
	"netbackup/internal/sshclient"
)

func main() {
	os.Exit(run())
}

// run contains the real logic and returns a process exit code, keeping
// main() itself trivial and keeping os.Exit (which skips deferred calls)
// out of the main control flow.
func run() int {
	inventoryPath := flag.String("inventory", "inventory.csv", "path to the device inventory CSV file")
	outputDir := flag.String("out", "backups", "directory to write backup files into")
	logDir := flag.String("log-dir", "logs", "directory to write run logs into")
	envFile := flag.String("env-file", ".env", "path to an optional .env file holding NETBACKUP_USERNAME / NETBACKUP_PASSWORD")
	concurrency := flag.Int("concurrency", 5, "maximum number of devices to back up in parallel")
	connectTimeout := flag.Duration("connect-timeout", 10*time.Second, "TCP connect + SSH handshake timeout per device")
	commandTimeout := flag.Duration("command-timeout", 30*time.Second, "time budget for running the backup command per device")
	redactSecrets := flag.Bool("redact", true, "mask passwords, pre-shared keys, and SNMP communities from saved files")
	saveUnchanged := flag.Bool("save-unchanged", false, "save a new backup file even if configuration is identical to the previous backup")
	retentionCount := flag.Int("retention-count", 0, "maximum number of recent backups to keep per device (0 to disable)")
	retentionDays := flag.Int("retention-days", 0, "prune backups older than N days per device (0 to disable)")
	knownHostsPath := flag.String("known-hosts", "known_hosts", "path to an OpenSSH known_hosts file used to verify device host keys")
	insecureIgnoreHostKey := flag.Bool("insecure-ignore-host-key", false, "DANGEROUS: skip host key verification entirely instead of checking -known-hosts (exposes connections to MITM)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger, err := logging.New(*logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		return 1
	}
	defer logger.Close()

	devices, warnings := inventory.Load(*inventoryPath)
	for _, w := range warnings {
		logger.Warn("inventory: %v", w)
	}
	if len(devices) == 0 {
		logger.Error("no usable devices found in %q -- nothing to do", *inventoryPath)
		return 1
	}
	logger.Info("loaded %d device(s) from %s", len(devices), *inventoryPath)

	credStore, err := credentials.NewStore(*envFile)
	if err != nil {
		logger.Error("resolving credentials: %v", err)
		return 1
	}

	var hostKeyCallback ssh.HostKeyCallback
	if *insecureIgnoreHostKey {
		logger.Warn("host key verification disabled via -insecure-ignore-host-key -- connections are vulnerable to MITM")
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		hostKeyCallback, err = sshclient.LoadKnownHosts(*knownHostsPath)
		if err != nil {
			logger.Error("loading known_hosts: %v (populate -known-hosts, or pass -insecure-ignore-host-key to explicitly disable verification)", err)
			return 1
		}
	}

	cfg := backup.Config{
		CredStore:      credStore,
		OutputDir:      *outputDir,
		Concurrency:    *concurrency,
		Redact:         *redactSecrets,
		RetentionCount: *retentionCount,
		RetentionDays:  *retentionDays,
		SaveUnchanged:  *saveUnchanged,
		SSH: sshclient.Options{
			ConnectTimeout:  *connectTimeout,
			CommandTimeout:  *commandTimeout,
			HostKeyCallback: hostKeyCallback,
		},
	}

	logger.Info("starting backup run: %d device(s), concurrency=%d, redact=%t, retention-count=%d, retention-days=%d",
		len(devices), cfg.Concurrency, cfg.Redact, cfg.RetentionCount, cfg.RetentionDays)

	results := backup.RunAll(ctx, devices, cfg)
	failures := logger.Summary(results)

	if failures > 0 {
		return 2 // distinct from the "fatal config error" exit code 1
	}
	return 0
}
