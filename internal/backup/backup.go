// Package backup ties inventory, credentials, vendor profiles and the SSH
// client together: for each device, connect, run the right backup command,
// write the result to disk, and report what happened -- without ever
// letting one device's failure stop the others.
package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"netbackup/internal/credentials"
	"netbackup/internal/errcat"
	"netbackup/internal/inventory"
	"netbackup/internal/sshclient"
	"netbackup/internal/vendor"
)

// Config holds everything a backup run needs besides the device list.
type Config struct {
	Creds       credentials.Credentials
	OutputDir   string
	Concurrency int
	SSH         sshclient.Options
}

// Result is what came of trying to back up one device.
type Result struct {
	Device     inventory.Device
	Success    bool
	Category   errcat.Category
	Err        error
	OutputPath string
	Duration   time.Duration
}

// RunAll backs up every device in the inventory using a bounded pool of
// workers, so a handful of unreachable devices can't serialise the whole
// run behind their timeouts, while a reasonable concurrency cap keeps the
// tool from hammering dozens of devices at once.
func RunAll(devices []inventory.Device, cfg Config) []Result {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}

	results := make([]Result, len(devices))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for i, dev := range devices {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, dev inventory.Device) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runOne(dev, cfg)
		}(i, dev)
	}

	wg.Wait()
	return results
}

// runOne performs a single device's backup. The recover() is defense in
// depth on top of the already-careful error handling below: a bug in a
// dependency, or an unexpected nil somewhere, still must not crash the
// whole batch -- it should surface as one failed device instead.
func runOne(dev inventory.Device, cfg Config) (result Result) {
	start := time.Now()
	result = Result{Device: dev}

	defer func() {
		if r := recover(); r != nil {
			result.Success = false
			result.Category = errcat.CategoryUnknown
			result.Err = fmt.Errorf("recovered from panic: %v", r)
		}
		result.Duration = time.Since(start)
	}()

	profile, err := vendor.Get(dev.Vendor)
	if err != nil {
		result.Err = err
		result.Category = errcat.CategoryUnknown
		return result
	}

	client, err := sshclient.Connect(dev.Address, dev.Port, cfg.Creds.Username, cfg.Creds.Password, cfg.SSH)
	if err != nil {
		cerr := errcat.Classify(dev.Hostname, "connect", err)
		result.Err = cerr
		result.Category = cerr.Category
		return result
	}
	defer client.Close()

	var output string
	if profile.Interactive {
		cmds := append(append([]string{}, profile.SetupCommands...), profile.BackupCommand)
		output, err = sshclient.RunInteractive(client, cmds, cfg.SSH)
	} else {
		output, err = sshclient.RunCommand(client, profile.BackupCommand, cfg.SSH)
	}
	if err != nil {
		cerr := errcat.Classify(dev.Hostname, "command", err)
		result.Err = cerr
		result.Category = cerr.Category
		return result
	}

	path, err := save(dev, profile, output, cfg.OutputDir)
	if err != nil {
		cerr := errcat.New(errcat.CategoryIOError, dev.Hostname, "write", err)
		result.Err = cerr
		result.Category = cerr.Category
		return result
	}

	result.Success = true
	result.OutputPath = path
	return result
}

// save writes the captured config to <outputDir>/<hostname>_<timestamp><ext>.
func save(dev inventory.Device, profile vendor.Profile, content, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return "", fmt.Errorf("creating output dir: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s_%s%s", sanitize(dev.Hostname), timestamp, profile.FileExtension)
	path := filepath.Join(outputDir, filename)

	if _, err := os.Stat(path); err == nil {
		filename = fmt.Sprintf("%s_%s_%s%s", sanitize(dev.Hostname), sanitize(dev.Address), timestamp, profile.FileExtension)
		path = filepath.Join(outputDir, filename)
	}

	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		return "", fmt.Errorf("writing backup file: %w", err)
	}
	return path, nil
}

func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
