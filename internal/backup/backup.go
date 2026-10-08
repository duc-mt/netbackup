// Package backup ties inventory, credentials, vendor profiles and the SSH
// client together: for each device, connect, run the right backup command,
// write the result to disk, and report what happened -- without ever
// letting one device's failure stop the others.
package backup

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"netbackup/internal/credentials"
	"netbackup/internal/errcat"
	"netbackup/internal/inventory"
	"netbackup/internal/redact"
	"netbackup/internal/sshclient"
	"netbackup/internal/vendor"
)

// Config holds everything a backup run needs besides the device list.
type Config struct {
	CredStore      *credentials.Store
	Creds          credentials.Credentials
	OutputDir      string
	RunName        string
	Concurrency    int
	Redact         bool
	RetentionCount int
	RetentionDays  int
	SaveUnchanged  bool
	SSH            sshclient.Options
}

func (c *Config) creds(group string) credentials.Credentials {
	if c.CredStore != nil {
		return c.CredStore.ForGroup(group)
	}
	return c.Creds
}

// Result is what came of trying to back up one device.
type Result struct {
	Device     inventory.Device
	Success    bool
	Unchanged  bool
	Category   errcat.Category
	Err        error
	OutputPath string
	Duration   time.Duration
}

// RunAll backs up every device in the inventory using a bounded pool of
// workers, so a handful of unreachable devices can't serialise the whole
// run behind their timeouts, while a reasonable concurrency cap keeps the
// tool from hammering dozens of devices at once.
func RunAll(ctx context.Context, devices []inventory.Device, cfg Config) []Result {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.RunName == "" {
		cfg.RunName = "backup-" + time.Now().Format("2006-01-02-1504")
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
			results[i] = runOne(ctx, dev, cfg)
		}(i, dev)
	}

	wg.Wait()
	return results
}

// runOne performs a single device's backup. The recover() is defense in
// depth on top of the already-careful error handling below: a bug in a
// dependency, or an unexpected nil somewhere, still must not crash the
// whole batch -- it should surface as one failed device instead.
func runOne(ctx context.Context, dev inventory.Device, cfg Config) (result Result) {
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
	dev.Vendor = profile.Key

	output, err := fetchConfig(ctx, dev, profile, cfg)
	if err != nil {
		if cerr, ok := err.(*errcat.Error); ok {
			result.Err = cerr
			result.Category = cerr.Category
		} else {
			result.Err = err
			result.Category = errcat.CategoryUnknown
		}
		return result
	}

	if cfg.Redact {
		output = redact.Config(output)
	}

	// Change Detection against latest backup
	latestPath, latestHash, found := findLatestBackup(cfg.OutputDir, dev, profile.FileExtension)
	currHash := sha256.Sum256([]byte(output))
	if found && latestHash == currHash {
		result.Unchanged = true
		if !cfg.SaveUnchanged {
			result.Success = true
			result.OutputPath = latestPath
			return result
		}
	}

	path, err := save(dev, profile, output, cfg)
	if err != nil {
		cerr := errcat.New(errcat.CategoryIOError, dev.Hostname, "write", err)
		result.Err = cerr
		result.Category = cerr.Category
		return result
	}

	if cfg.RetentionCount > 0 || cfg.RetentionDays > 0 {
		_ = pruneBackups(cfg.OutputDir, dev, profile.FileExtension, cfg.RetentionCount, cfg.RetentionDays)
	}

	result.Success = true
	result.OutputPath = path
	return result
}

func fetchConfig(ctx context.Context, dev inventory.Device, profile vendor.Profile, cfg Config) (string, error) {
	creds := cfg.creds(dev.CredentialGroup)
	client, err := sshclient.Connect(ctx, dev.Address, dev.Port, creds.Username, creds.Password, cfg.SSH)
	if err != nil {
		return "", errcat.Classify(dev.Hostname, "connect", err)
	}
	defer client.Close()

	var output string
	if profile.Interactive {
		cmds := make([]string, 0, len(profile.SetupCommands)+1)
		cmds = append(cmds, profile.SetupCommands...)
		cmds = append(cmds, profile.BackupCommand)
		output, err = sshclient.RunInteractive(client, cmds, cfg.SSH)
	} else {
		output, err = sshclient.RunCommand(client, profile.BackupCommand, cfg.SSH)
	}
	if err != nil {
		return "", errcat.Classify(dev.Hostname, "command", err)
	}
	return output, nil
}

// save writes the captured config to <outputDir>/<runName>/<vendor>/<hostname><ext>.
func save(dev inventory.Device, profile vendor.Profile, content string, cfg Config) (string, error) {
	vendorDir := filepath.Join(cfg.OutputDir, cfg.RunName, dev.Vendor)
	if err := os.MkdirAll(vendorDir, 0o750); err != nil {
		return "", fmt.Errorf("creating vendor dir: %w", err)
	}

	filename := sanitize(dev.Hostname) + profile.FileExtension
	path := filepath.Join(vendorDir, filename)

	if _, err := os.Stat(path); err == nil {
		// Collision fallback
		filename = fmt.Sprintf("%s_%s%s", sanitize(dev.Hostname), sanitize(dev.Address), profile.FileExtension)
		path = filepath.Join(vendorDir, filename)
	}

	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		return "", fmt.Errorf("writing backup file: %w", err)
	}
	return path, nil
}

type fileEntry struct {
	path    string
	modTime time.Time
}

func findMatchingBackups(outputDir string, dev inventory.Device, ext string) ([]fileEntry, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, err
	}

	var matching []fileEntry
	filename := sanitize(dev.Hostname) + ext
	filenameAltPrefix := sanitize(dev.Hostname) + "_"
	vCandidates := vendorCandidates(dev)

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "backup-") {
			continue
		}

		for _, vName := range vCandidates {
			vendorDir := filepath.Join(outputDir, entry.Name(), vName)
			ventries, err := os.ReadDir(vendorDir)
			if err != nil {
				continue
			}

			for _, v := range ventries {
				if v.IsDir() {
					continue
				}
				if v.Name() == filename || (strings.HasPrefix(v.Name(), filenameAltPrefix) && strings.HasSuffix(v.Name(), ext)) {
					info, err := v.Info()
					if err == nil {
						matching = append(matching, fileEntry{
							path:    filepath.Join(vendorDir, v.Name()),
							modTime: info.ModTime(),
						})
					}
				}
			}
		}
	}

	if len(matching) == 0 {
		return nil, nil
	}

	// Sort newest first
	slices.SortFunc(matching, func(a, b fileEntry) int {
		return b.modTime.Compare(a.modTime)
	})

	return matching, nil
}

func findLatestBackup(outputDir string, dev inventory.Device, ext string) (string, [32]byte, bool) {
	matching, err := findMatchingBackups(outputDir, dev, ext)
	if err != nil || len(matching) == 0 {
		return "", [32]byte{}, false
	}

	latestPath := matching[0].path
	data, err := os.ReadFile(latestPath)
	if err != nil {
		return "", [32]byte{}, false
	}

	return latestPath, sha256.Sum256(data), true
}

func pruneBackups(outputDir string, dev inventory.Device, ext string, keepCount, keepDays int) int {
	matching, err := findMatchingBackups(outputDir, dev, ext)
	if err != nil || len(matching) == 0 {
		return 0
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	pruned := 0

	for i, f := range matching {
		deleteFile := false
		// Keep at least the latest backup even if expired by days
		if i > 0 && keepDays > 0 && f.modTime.Before(cutoff) {
			deleteFile = true
		}
		if keepCount > 0 && i >= keepCount {
			deleteFile = true
		}

		if deleteFile {
			if err := os.Remove(f.path); err == nil {
				pruned++
				// Attempt to clean up empty directories
				dir := filepath.Dir(f.path)
				if removeEmptyDir(dir) {
					parentDir := filepath.Dir(dir)
					removeEmptyDir(parentDir)
				}
			}
		}
	}

	return pruned
}

func removeEmptyDir(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	_, readErr := f.Readdirnames(1)
	_ = f.Close()
	if readErr == nil {
		return false
	}
	_ = os.Remove(dir)
	return true
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

func vendorCandidates(dev inventory.Device) []string {
	keys := []string{dev.Vendor}
	if p, err := vendor.Get(dev.Vendor); err == nil && p.Key != "" && p.Key != dev.Vendor {
		keys = append(keys, p.Key)
	}
	return keys
}
