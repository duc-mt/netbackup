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
	"netbackup/internal/compliance"
	"netbackup/internal/credentials"
	"netbackup/internal/inventory"
	"netbackup/internal/logging"
	"netbackup/internal/report"
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
	resultTable           bool
	showDiff              bool

	// Semantic diff, compliance and reports
	ignoreVolatile   bool   // drop volatile lines (timestamps, clock stamps) before diffing
	volatilePatterns string // optional file of extra volatile-line regexes
	goldenDir        string // directory of <vendor>.golden baselines
	complianceRules  string // single rules file with [vendor] sections
	htmlReport       string // path of the HTML run report ("" = none)
	mdReport         string // path of the Markdown changelog ("" = none)

	keyFile          string // path to SSH private key file (overrides NETBACKUP_KEY_FILE)
	retries          int    // number of retry attempts per device
	retryDelay       time.Duration // pause between retries

	// Jump host / bastion support
	jumpEnabled  bool   // master on/off switch
	jumpHost     string // host[:port] -- global default bastion
	jumpUser     string // bastion SSH username (overrides NETBACKUP_JUMP_USERNAME)
	jumpPassword string // bastion SSH password (overrides NETBACKUP_JUMP_PASSWORD)
	jumpKeyFile  string // bastion SSH private key file (overrides NETBACKUP_JUMP_KEY_FILE)
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.inventoryPath, "inventory", "inventory.csv", "path to the device inventory CSV file")
	flag.StringVar(&opts.outputDir, "out", "backups", "directory to write backup files into")
	flag.StringVar(&opts.logDir, "log-dir", "logs", "directory to write run logs into")
	flag.StringVar(&opts.logFormat, "log-format", "text", "log output format (text or json)")
	flag.StringVar(&opts.envFile, "env-file", ".env", "path to an optional .env file holding NETBACKUP_USERNAME / NETBACKUP_PASSWORD")
	flag.StringVar(&opts.keyFile, "key-file", "", "path to an SSH private key file for device authentication (overrides NETBACKUP_KEY_FILE)")
	flag.IntVar(&opts.retries, "retries", 0, "number of retry attempts per device upon failure (default: 0)")
	flag.DurationVar(&opts.retryDelay, "retry-delay", 2*time.Second, "delay between retries per device (default: 2s)")
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
	flag.BoolVar(&opts.resultTable, "result-table", true, "print a colorized per-device result table to stdout after the run")
	flag.BoolVar(&opts.showDiff, "diff", true, "display colorized diff against previous backup when configuration changes")
	flag.BoolVar(&opts.showDiff, "show-diff", true, "alias for -diff")
	flag.BoolVar(&opts.ignoreVolatile, "ignore-volatile", true, "ignore volatile lines (\"! Last configuration change at\", \"## Last commit\", ntp clock-period, ...) when detecting changes; saved files are never modified")
	flag.StringVar(&opts.volatilePatterns, "volatile-patterns", "", "optional file with extra volatile-line regular expressions, one per line (# starts a comment)")
	flag.StringVar(&opts.goldenDir, "golden-dir", "", "directory of <vendor>.golden baseline files (plus optional common.golden); enables golden-config compliance checks")
	flag.StringVar(&opts.complianceRules, "compliance-rules", "", "single compliance rules file with optional [vendor] sections; can be combined with -golden-dir")
	flag.StringVar(&opts.htmlReport, "html-report", "", "write a standalone HTML run report to this path ({run}, {timestamp} and {date} are expanded)")
	flag.StringVar(&opts.mdReport, "md-report", "", "write/update a Markdown changelog at this path, newest run first ({run}, {timestamp} and {date} are expanded)")

	// Jump host / bastion
	flag.BoolVar(&opts.jumpEnabled, "jump", true, "enable jump host / bastion routing (set false to connect all devices directly, overriding inventory and env config)")
	flag.StringVar(&opts.jumpHost, "jump-host", "", "global default bastion host[:port] for devices not reachable directly (e.g. mgmt-gw.example.com:22)")
	flag.StringVar(&opts.jumpUser, "jump-user", "", "SSH username for the global jump host (overrides NETBACKUP_JUMP_USERNAME)")
	flag.StringVar(&opts.jumpPassword, "jump-password", "", "SSH password for the global jump host (overrides NETBACKUP_JUMP_PASSWORD)")
	flag.StringVar(&opts.jumpKeyFile, "jump-key-file", "", "SSH private key file for the global jump host (overrides NETBACKUP_JUMP_KEY_FILE)")

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

	// Inject CLI credential flags into the environment before NewStore reads them,
	// so credential resolution stays in one place (the credentials package).
	if opts.keyFile != "" {
		os.Setenv(credentials.EnvKeyFile, opts.keyFile)
	}
	if opts.jumpUser != "" {
		os.Setenv(credentials.EnvJumpUsername, opts.jumpUser)
	}
	if opts.jumpPassword != "" {
		os.Setenv(credentials.EnvJumpPassword, opts.jumpPassword)
	}
	if opts.jumpKeyFile != "" {
		os.Setenv(credentials.EnvJumpKeyFile, opts.jumpKeyFile)
	}
	if opts.jumpHost != "" {
		os.Setenv(credentials.EnvJumpHost, opts.jumpHost)
	}

	credStore, err := credentials.NewStore(opts.envFile)
	if err != nil {
		logger.Error("resolving credentials: %v", err)
		return 1
	}

	if opts.jumpHost != "" && opts.jumpEnabled {
		logger.Info("global jump host configured: %s (per-device overrides via inventory jump_host column; disable with -jump=false)", opts.jumpHost)
	} else if !opts.jumpEnabled {
		logger.Info("jump host routing disabled via -jump=false -- all devices will be connected directly")
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
		CredStore:       credStore,
		OutputDir:       opts.outputDir,
		RunName:         backup.DefaultRunName(time.Now()),
		IgnoreVolatile:  opts.ignoreVolatile,
		Concurrency:     opts.concurrency,
		Redact:          opts.redactSecrets,
		RetentionCount:  opts.retentionCount,
		RetentionDays:   opts.retentionDays,
		SaveUnchanged:   opts.saveUnchanged,
		Retries:         opts.retries,
		RetryDelay:      opts.retryDelay,
		ShowDiff:        opts.showDiff,
		JumpEnabled:     opts.jumpEnabled,
		JumpDefaultHost: opts.jumpHost,
		SSH: sshclient.Options{
			ConnectTimeout:  opts.connectTimeout,
			CommandTimeout:  opts.commandTimeout,
			HostKeyCallback: hostKeyCallback,
		},
	}

	configureSemanticDiff(logger, &cfg, opts)
	configureCompliance(logger, &cfg, opts)

	if opts.showDiff {
		cfg.OnDiff = func(dev inventory.Device, lines []string, added, removed int) {
			logger.Info("[%s] configuration changes detected (+%d/-%d):", dev.Hostname, added, removed)
			logger.DiffLines(dev.Hostname, "  ", lines)
		}
	}

	logger.Info("starting backup run: %d device(s), concurrency=%d, redact=%t, retention-count=%d, retention-days=%d",
		len(devices), cfg.Concurrency, cfg.Redact, cfg.RetentionCount, cfg.RetentionDays)

	results := backup.RunAll(ctx, devices, cfg)
	failures := logger.Summary(results)

	// The log summary above is for the run log (file + stderr, plain
	// text, machine-greppable). This table is a separate, human-facing
	// view on stdout; it never aborts the run even if rendering itself
	// goes wrong, so it's safe to always attempt.
	rows := report.FromResults(results)
	if opts.resultTable {
		report.PrintSummaryTable(rows)
	}

	// Shareable reports. Like the table, they never abort the run: any
	// failure is logged as a warning and the exit code is unaffected.
	writeReports(logger, opts, cfg.RunName, rows)

	if failures > 0 {
		return 2 // distinct from the "fatal config error" exit code 1
	}
	return 0
}

// configureSemanticDiff wires the volatile-line filter into the backup
// config. A bad pattern or an unreadable patterns file only costs the
// extra patterns; the built-in rules always stay active.
func configureSemanticDiff(logger *logging.Logger, cfg *backup.Config, opts cliOptions) {
	if !opts.ignoreVolatile {
		logger.Info("semantic diff disabled (-ignore-volatile=false): volatile lines such as timestamps count as changes")
		return
	}

	var extra []string
	if opts.volatilePatterns != "" {
		patterns, err := backup.LoadVolatilePatterns(opts.volatilePatterns)
		if err != nil {
			logger.Warn("volatile patterns: %v -- continuing with the built-in rules only", err)
		} else {
			extra = patterns
		}
	}

	filter, errs := backup.NewVolatileFilter(extra)
	for _, err := range errs {
		logger.Warn("%v -- pattern ignored", err)
	}
	cfg.VolatileFilter = filter
	logger.Info("semantic diff: ignoring volatile lines (%d rule(s) active)", filter.Len())
}

// configureCompliance loads the golden baselines when requested. Problems
// are warnings, never fatal: an unreadable baseline must not stop the
// nightly backups, it only turns compliance checking off.
func configureCompliance(logger *logging.Logger, cfg *backup.Config, opts cliOptions) {
	if opts.goldenDir == "" && opts.complianceRules == "" {
		return
	}

	baseline, errs := compliance.Load(opts.goldenDir, opts.complianceRules)
	for _, err := range errs {
		logger.Warn("compliance: %v", err)
	}
	if baseline == nil {
		logger.Warn("compliance checking disabled: no usable baseline rules were loaded")
		return
	}
	cfg.Baseline = baseline
	logger.Info("compliance: checking devices against %d golden baseline rule(s)", baseline.Len())
}

// writeReports produces the optional HTML and Markdown reports.
func writeReports(logger *logging.Logger, opts cliOptions, runName string, rows []report.BackupResult) {
	info := report.RunInfo{RunName: runName, GeneratedAt: time.Now()}

	if opts.htmlReport != "" {
		path := report.ExpandPath(opts.htmlReport, info)
		emitReport(logger, "HTML", path, func() ([]string, error) {
			return report.WriteHTML(path, info, rows)
		})
	}
	if opts.mdReport != "" {
		path := report.ExpandPath(opts.mdReport, info)
		emitReport(logger, "Markdown", path, func() ([]string, error) {
			return report.WriteMarkdown(path, info, rows)
		})
	}
}

// emitReport runs one report writer, logging its warnings and outcome. A
// panic inside the writer is recovered and logged so a reporting bug can
// never turn a finished backup run into a crash.
func emitReport(logger *logging.Logger, kind, path string, write func() ([]string, error)) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warn("%s report generation failed: %v", kind, r)
		}
	}()

	warnings, err := write()
	for _, w := range warnings {
		logger.Warn("%s", w)
	}
	if err != nil {
		logger.Warn("%s report not written to %s: %v", kind, path, err)
		return
	}
	logger.Info("%s report written to %s", kind, path)
}
