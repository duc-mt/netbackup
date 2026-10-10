// ==============================================================================
// Package main implements Implementation and logic for main..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run main.go [options]
// Notes:         Go package implementation
// ==============================================================================
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

type cliOptions struct {
	inventoryPath         string
	outputDir             string
	logDir                string
	logFormat             string
	envFile               string
	concurrency           int
	connectTimeout        time.Duration
	commandTimeout        time.Duration
	redactSecrets         bool
	saveUnchanged         bool
	retentionCount        int
	retentionDays         int
	knownHostsPath        string
	knownHostsPolicy      string
	insecureIgnoreHostKey bool
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.inventoryPath, "inventory", "inventory.csv", "path to the device inventory CSV file")
	flag.StringVar(&opts.outputDir, "out", "backups", "directory to write backup files into")
	flag.StringVar(&opts.logDir, "log-dir", "logs", "directory to write run logs into")
	flag.StringVar(&opts.logFormat, "log-format", "text", "log output format (text or json)")
	flag.StringVar(&opts.envFile, "env-file", ".env", "path to an optional .env file holding NETBACKUP_USERNAME / NETBACKUP_PASSWORD")
	flag.IntVar(&opts.concurrency, "concurrency", 5, "maximum number of devices to back up in parallel")
	flag.DurationVar(&opts.connectTimeout, "connect-timeout", 10*time.Second, "TCP connect + SSH handshake timeout per device")
	flag.DurationVar(&opts.commandTimeout, "command-timeout", 30*time.Second, "time budget for running the backup command per device")
	flag.BoolVar(&opts.redactSecrets, "redact", true, "mask passwords, pre-shared keys, and SNMP communities from saved files")
	flag.BoolVar(&opts.saveUnchanged, "save-unchanged", false, "save a new backup file even if configuration is identical to the previous backup")
	flag.IntVar(&opts.retentionCount, "retention-count", 0, "maximum number of recent backups to keep per device (0 to disable)")
	flag.IntVar(&opts.retentionDays, "retention-days", 0, "prune backups older than N days per device (0 to disable)")
	flag.StringVar(&opts.knownHostsPath, "known-hosts", "known_hosts", "path to an OpenSSH known_hosts file used to verify device host keys")
	flag.StringVar(&opts.knownHostsPolicy, "known-hosts-policy", "strict", "policy for unknown host keys: 'strict' or 'accept-new'")
	flag.BoolVar(&opts.insecureIgnoreHostKey, "insecure-ignore-host-key", false, "DANGEROUS: skip host key verification entirely instead of checking -known-hosts (exposes connections to MITM)")
	flag.Parse()
	return opts
}

// run contains the real logic and returns a process exit code, keeping
// main() itself trivial and keeping os.Exit (which skips deferred calls)
// out of the main control flow.
func run() int {
	opts := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger, err := logging.New(opts.logDir, opts.logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		return 1
	}
	defer logger.Close()

	devices, warnings := inventory.Load(opts.inventoryPath)
	for _, w := range warnings {
		logger.Warn("inventory: %v", w)
	}
	if len(devices) == 0 {
		logger.Error("no usable devices found in %q -- nothing to do", opts.inventoryPath)
		return 1
	}
	logger.Info("loaded %d device(s) from %s", len(devices), opts.inventoryPath)

	credStore, err := credentials.NewStore(opts.envFile)
	if err != nil {
		logger.Error("resolving credentials: %v", err)
		return 1
	}

	var hostKeyCallback ssh.HostKeyCallback
	if opts.insecureIgnoreHostKey {
		logger.Warn("host key verification disabled via -insecure-ignore-host-key -- connections are vulnerable to MITM")
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		policy := sshclient.KnownHostsPolicy(opts.knownHostsPolicy)
		hostKeyCallback, err = sshclient.KnownHostsCallback(opts.knownHostsPath, policy)
		if err != nil {
			logger.Error("loading known_hosts: %v", err)
			return 1
		}
	}

	cfg := backup.Config{
		CredStore:      credStore,
		OutputDir:      opts.outputDir,
		Concurrency:    opts.concurrency,
		Redact:         opts.redactSecrets,
		RetentionCount: opts.retentionCount,
		RetentionDays:  opts.retentionDays,
		SaveUnchanged:  opts.saveUnchanged,
		SSH: sshclient.Options{
			ConnectTimeout:  opts.connectTimeout,
			CommandTimeout:  opts.commandTimeout,
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
