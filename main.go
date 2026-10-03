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
	"flag"
	"fmt"
	"os"
	"time"

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
	flag.Parse()

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

	creds, err := credentials.Resolve(*envFile)
	if err != nil {
		logger.Error("resolving credentials: %v", err)
		return 1
	}

	cfg := backup.Config{
		Creds:       creds,
		OutputDir:   *outputDir,
		Concurrency: *concurrency,
		SSH: sshclient.Options{
			ConnectTimeout: *connectTimeout,
			CommandTimeout: *commandTimeout,
		},
	}

	logger.Info("starting backup run: %d device(s), concurrency=%d, connect-timeout=%s, command-timeout=%s",
		len(devices), cfg.Concurrency, *connectTimeout, *commandTimeout)

	results := backup.RunAll(devices, cfg)
	failures := logger.Summary(results)

	if failures > 0 {
		return 2 // distinct from the "fatal config error" exit code 1
	}
	return 0
}
