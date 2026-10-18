// ==============================================================================
// Package main implements Implementation and logic for backup..
// Author:        Mai Tan Duc <ducmai.network@gmail.com>
// Created:       2026-10-10
// Version:       1.0.0
// License:       MIT
// ==============================================================================
// Usage:         go run backup.go [options]
// Notes:         Go package implementation
// ==============================================================================
// Package backup ties inventory, credentials, vendor profiles and the SSH
// client together: for each device, connect, run the right backup command,
// write the result to disk, and report what happened -- without ever
// letting one device's failure stop the others.
package backup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"netbackup/internal/compliance"
	"netbackup/internal/credentials"
	"netbackup/internal/errcat"
	"netbackup/internal/inventory"
	"netbackup/internal/memory"
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
	Retries        int           // number of retry attempts per device upon failure (default: 0)
	RetryDelay     time.Duration // pause between retries (default: 2s)
	DialJitter     time.Duration // random jitter delay up to this value applied before connection (default: 0)
	SSH            sshclient.Options
	// DryRun tests connection and authentication without executing commands or saving configs.
	DryRun bool

	// JumpEnabled globally enables or disables bastion / jump host routing.
	// When false every device is connected directly, ignoring both the
	// inventory jump_host column and JumpDefaultHost (useful for running from
	// the management server itself, or for debugging connectivity issues).
	JumpEnabled bool

	// JumpDefaultHost is the global CLI -jump-host value (host[:port]).
	// It is the fallback when a device's inventory jump_host name has no
	// matching NETBACKUP_JUMP_<NAME>_HOST env var.  Empty = no global jump.
	JumpDefaultHost string

	// ShowDiff controls whether configuration diffs are calculated and logged
	ShowDiff bool

	// OnDiff callback is invoked when a diff is detected against the latest backup
	OnDiff func(dev inventory.Device, lines []string, added, removed int)

	// OnProgress callback is invoked each time a device backup finishes.
	OnProgress func(completed, total int, res Result)

	// IgnoreVolatile makes change detection compare the "before" and "after"
	// configurations with volatile lines (clock stamps, "last changed"
	// comments, size counters, uptime banners) removed, so noise alone is
	// not reported as a change. The file written to disk is never filtered.
	IgnoreVolatile bool

	// VolatileFilter supplies the volatile-line rules used when
	// IgnoreVolatile is set. Nil means the built-in rules.
	VolatileFilter *VolatileFilter

	// Baseline holds the golden-config rules every device is checked
	// against. Nil disables compliance checking.
	Baseline *compliance.Baseline

	// Route memory & auto-fallback
	MemStore            *memory.Store
	AutoFallbackDevices []inventory.Device
}

// jumpPool maintains a lazily-initialised, shared *ssh.Client per unique
// bastion address and persists learned route memory.
type jumpPool struct {
	mu          sync.Mutex
	clients     map[string]*ssh.Client // keyed by "host:port"
	badBastions map[string]bool        // hosts that failed direct connection
	memStore    *memory.Store
	opts        sshclient.Options
}

func newJumpPool(opts sshclient.Options, memStore *memory.Store) *jumpPool {
	return &jumpPool{
		clients:     make(map[string]*ssh.Client),
		badBastions: make(map[string]bool),
		memStore:    memStore,
		opts:        opts,
	}
}

func (p *jumpPool) markBadBastion(addr string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.badBastions[addr] = true
}

func (p *jumpPool) isBadBastion(addr string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.badBastions[addr]
}

// get returns (or lazily creates) the bastion client for addr.
// addr must be in "host:port" form.
func (p *jumpPool) get(ctx context.Context, addr string, auth sshclient.AuthOptions) (*ssh.Client, error) {
	p.mu.Lock()
	if c, ok := p.clients[addr]; ok {
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	// Parse host:port
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Treat as host-only with default port 22
		host = addr
		portStr = "22"
	}
	port := 22
	if n, err := fmt.Sscanf(portStr, "%d", &port); n != 1 || err != nil {
		port = 22
	}

	c, err := sshclient.ConnectJumpHostWithAuth(ctx, host, port, auth, p.opts)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// Re-check in case another goroutine raced us.
	if existing, ok := p.clients[addr]; ok {
		c.Close()
		return existing, nil
	}
	p.clients[addr] = c
	return c, nil
}

// closeAll closes every cached bastion client.
func (p *jumpPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for addr, c := range p.clients {
		c.Close()
		delete(p.clients, addr)
	}
}

func (c *Config) creds(group string) credentials.Credentials {
	if c.CredStore != nil {
		return c.CredStore.ForGroup(group)
	}
	return c.Creds
}

// Result is what came of trying to back up one device.
type Result struct {
	Device       inventory.Device
	Success      bool
	DryRun       bool
	Unchanged    bool
	DiffLines    []string
	DiffAdded    int
	DiffRemoved  int
	HasDiff      bool
	Category     errcat.Category
	Err          error
	OutputPath   string
	DiffPath     string
	Duration     time.Duration
	Checksum     string // SHA-256 hash of configuration
	BytesWritten int64  // bytes written to disk (0 if skipped or failed)

	// VolatileOnly is set when the raw output differs from the previous
	// backup but only in volatile (or whitespace-only) lines, so the device
	// counts as Unchanged. Only possible with Config.IgnoreVolatile.
	VolatileOnly bool

	// Risk is the classification of DiffLines; Risk.Level is the highest
	// risk detected (RiskNone when nothing noteworthy changed).
	Risk RiskAssessment

	// Compliance is the golden-config check of the fetched configuration.
	// Compliance.Checked is false when no baseline applies.
	Compliance compliance.Report

	// Warnings lists non-fatal problems met while processing this device
	// (a filter, classifier or compliance failure, a diff skipped as too
	// large). They degrade the extra analysis only; the backup itself
	// still succeeds.
	Warnings []string
}

// DefaultRunName is the run directory name RunAll uses when Config.RunName
// is empty. Callers that need to know the name before the run starts (for
// example to put it in a report path) can set Config.RunName from it.
func DefaultRunName(t time.Time) string {
	return "backup-" + t.Format("2006-01-02-1504")
}

func calcJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

// ipLocker serializes backup operations per target IP address, preventing
// parallel workers from overwhelming per-device limits such as JunOS
// connection-limit (default: 10) or rate-limit (default: 5).
type ipLocker struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newIPLocker() *ipLocker {
	return &ipLocker{
		locks: make(map[string]*sync.Mutex),
	}
}

func (l *ipLocker) lock(ip string) func() {
	if ip == "" {
		return func() {}
	}
	l.mu.Lock()
	m, ok := l.locks[ip]
	if !ok {
		m = &sync.Mutex{}
		l.locks[ip] = m
	}
	l.mu.Unlock()

	m.Lock()
	return func() {
		m.Unlock()
	}
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
		cfg.RunName = DefaultRunName(time.Now())
	}

	pool := newJumpPool(cfg.SSH, cfg.MemStore)
	defer pool.closeAll()

	locker := newIPLocker()
	results := make([]Result, len(devices))
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	var completed atomic.Int32

	for i, dev := range devices {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, dev inventory.Device) {
			defer wg.Done()
			defer func() { <-sem }()

			unlock := locker.lock(dev.Address)
			defer unlock()

			if cfg.DialJitter > 0 {
				jitter := calcJitter(cfg.DialJitter)
				select {
				case <-ctx.Done():
					results[i] = Result{
						Device:   dev,
						Success:  false,
						Category: errcat.CategoryUnknown,
						Err:      ctx.Err(),
					}
					if cfg.OnProgress != nil {
						cfg.OnProgress(int(completed.Add(1)), len(devices), results[i])
					}
					return
				case <-time.After(jitter):
				}
			}
			res := runOne(ctx, dev, cfg, pool)
			results[i] = res
			if cfg.OnProgress != nil {
				cfg.OnProgress(int(completed.Add(1)), len(devices), res)
			}
		}(i, dev)
	}

	wg.Wait()
	return results
}

// runOne performs a single device's backup. The recover() is defense in
// depth on top of the already-careful error handling below: a bug in a
// dependency, or an unexpected nil somewhere, still must not crash the
// whole batch -- it should surface as one failed device instead.
func runOne(ctx context.Context, dev inventory.Device, cfg Config, pool *jumpPool) (result Result) {
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

	maxAttempts := cfg.Retries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	baseDelay := cfg.RetryDelay
	if baseDelay <= 0 {
		baseDelay = 2 * time.Second
	}

	var output string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if cfg.DryRun {
			var client *ssh.Client
			client, err = connectDevice(ctx, dev, cfg, pool)
			if err == nil {
				client.Close()
				break
			}
		} else {
			output, err = fetchConfig(ctx, dev, profile, cfg, pool)
			if err == nil {
				break
			}
		}

		// If cancelled, do not retry
		if ctx.Err() != nil {
			break
		}

		// If we encountered a transient rate-limit / connection-limit drop (e.g. JunOS handshake EOF
		// or connection reset), dynamically allow 1 automatic retry even if cfg.Retries is 0,
		// and use an extended jittered backoff (3.0s - 4.5s) to allow device token buckets to replenish
		// and half-closed sockets to drain.
		isRateLimit := isTransientRateLimitDrop(err)
		if isRateLimit && attempt == maxAttempts && cfg.Retries == 0 && maxAttempts == 1 {
			maxAttempts = 2
		}

		if attempt < maxAttempts {
			delay := baseDelay
			if isRateLimit {
				delay = 3*time.Second + calcJitter(1500*time.Millisecond)
			}
			select {
			case <-ctx.Done():
				break
			case <-time.After(delay):
			}
		}
	}

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

	if cfg.DryRun {
		result.Success = true
		result.DryRun = true
		return result
	}

	return processConfig(dev, profile, output, cfg, result)
}

// processConfig does everything that happens locally once a device's
// configuration has been fetched: redaction, golden-config compliance,
// change detection (volatile-aware), risk classification, saving and
// retention. It is separate from runOne so it can be exercised without an
// SSH connection.
//
// The analysis steps (volatile filtering, diffing, risk classification,
// compliance) are best effort: if one fails it is recorded in
// Result.Warnings and the backup carries on, because a problem in an extra
// feature must never cost the operator the backup itself.
func processConfig(dev inventory.Device, profile vendor.Profile, output string, cfg Config, result Result) Result {
	if cfg.Redact {
		output = redact.Config(output)
	}

	report, warn := checkCompliance(cfg.Baseline, profile.Key, output)
	result.Compliance = report
	if warn != "" {
		result.Warnings = append(result.Warnings, warn)
	}

	// Change Detection against latest backup
	latestPath, latestHash, found := findLatestBackup(cfg.OutputDir, dev, profile.FileExtension)
	currHash := sha256.Sum256([]byte(output))
	result.Checksum = hex.EncodeToString(currHash[:])
	if found && latestHash == currHash {
		result.Unchanged = true
		if !cfg.SaveUnchanged {
			result.Success = true
			result.OutputPath = latestPath
			result.BytesWritten = 0
			return result
		}
	}

	if found && latestHash != currHash {
		beforeData, err := os.ReadFile(latestPath)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("change detection skipped: reading previous backup %s: %v", latestPath, err))
		} else {
			dLines, added, removed, ok, warn := compareConfigs(cfg, profile.Key, string(beforeData), output)
			if warn != "" {
				result.Warnings = append(result.Warnings, warn)
			}
			switch {
			case !ok:
				// No diff available: treat as changed and save the new file.
			case len(dLines) == 0 && cfg.IgnoreVolatile:
				// The raw text differs but nothing meaningful does.
				result.Unchanged = true
				result.VolatileOnly = true
				if !cfg.SaveUnchanged {
					result.Success = true
					result.OutputPath = latestPath
					result.BytesWritten = 0
					return result
				}
			case len(dLines) > 0:
				result.DiffLines = dLines
				result.DiffAdded = added
				result.DiffRemoved = removed
				result.HasDiff = true

				risk, warn := assessRiskSafe(dLines)
				result.Risk = risk
				if warn != "" {
					result.Warnings = append(result.Warnings, warn)
				}

				if cfg.OnDiff != nil {
					cfg.OnDiff(dev, dLines, added, removed)
				}
			}
		}
	}

	// The saved file is always the real configuration (redacted if
	// requested) -- never the volatile-filtered comparison text.
	path, err := save(dev, profile, output, cfg)
	if err != nil {
		cerr := errcat.New(errcat.CategoryIOError, dev.Hostname, "write", err)
		result.Err = cerr
		result.Category = cerr.Category
		return result
	}
	result.BytesWritten = int64(len(output))

	if result.HasDiff && len(result.DiffLines) > 0 {
		diffPath := strings.TrimSuffix(path, profile.FileExtension) + ".diff"
		if err := WriteFileAtomic(diffPath, []byte(strings.Join(result.DiffLines, "\n")+"\n"), 0o600); err == nil {
			result.DiffPath = diffPath
		}
	}

	if cfg.RetentionCount > 0 || cfg.RetentionDays > 0 {
		_ = pruneBackups(cfg.OutputDir, dev, profile.FileExtension, cfg.RetentionCount, cfg.RetentionDays)
	}

	result.Success = true
	result.OutputPath = path
	return result
}

// compareConfigs diffs the previous and the new configuration. With
// cfg.IgnoreVolatile both sides first pass through the volatile filter, so
// lines such as "! Last configuration change at ..." cannot produce a
// diff. ok is false (with a warning) when no diff could be produced, either
// because the input is too large or because something panicked.
func compareConfigs(cfg Config, vendorKey, before, after string) (lines []string, added, removed int, ok bool, warn string) {
	defer func() {
		if r := recover(); r != nil {
			lines, added, removed, ok = nil, 0, 0, false
			warn = fmt.Sprintf("change detection failed: %v", r)
		}
	}()

	if cfg.IgnoreVolatile {
		f := cfg.VolatileFilter
		if f == nil {
			f = sharedDefaultFilter()
		}
		before = f.Apply(vendorKey, before)
		after = f.Apply(vendorKey, after)
	}

	lines, added, removed, ok = diffConfig(before, after)
	if !ok {
		warn = "change detection skipped: configuration too large to diff"
	}
	return lines, added, removed, ok, warn
}

// assessRiskSafe classifies a diff, converting a panic into a warning.
func assessRiskSafe(lines []string) (a RiskAssessment, warn string) {
	defer func() {
		if r := recover(); r != nil {
			a = RiskAssessment{}
			warn = fmt.Sprintf("risk classification failed: %v", r)
		}
	}()
	return AssessRisk(lines), ""
}

// checkCompliance runs the golden-config check, converting a panic into a
// warning. A nil baseline yields an unchecked (zero) report.
func checkCompliance(b *compliance.Baseline, vendorKey, config string) (rep compliance.Report, warn string) {
	if b == nil {
		return compliance.Report{}, ""
	}
	defer func() {
		if r := recover(); r != nil {
			rep = compliance.Report{}
			warn = fmt.Sprintf("compliance check failed: %v", r)
		}
	}()
	return b.Check(vendorKey, config), ""
}

// resolveJumpAddr returns the effective bastion address for a device, using
// the following priority:
//  1. Per-device inventory jump_host name → NETBACKUP_JUMP_<NAME>_HOST env var
//  2. Per-device inventory jump_host name → same name used verbatim as host[:port]
//  3. cfg.JumpDefaultHost (from the -jump-host CLI flag)
//  4. Global NETBACKUP_JUMP_HOST env var (already in store.JumpDefaults().Host)
//
// Returns "" if jump is disabled (cfg.JumpEnabled == false) or no jump host
// is configured for this device.
func resolveJumpAddr(dev inventory.Device, cfg Config) string {
	if !cfg.JumpEnabled {
		return "" // jump globally disabled; connect every device directly
	}
	if dev.JumpHost != "" {
		if cfg.CredStore != nil {
			jc := cfg.CredStore.ForJumpHost(dev.JumpHost)
			if jc.Host != "" {
				return jc.Host
			}
		}
		// Treat the inventory value itself as host[:port]
		return dev.JumpHost
	}
	// Fall back to global CLI flag / env
	if cfg.JumpDefaultHost != "" {
		return cfg.JumpDefaultHost
	}
	if cfg.CredStore != nil {
		if h := cfg.CredStore.JumpDefaults().Host; h != "" {
			return h
		}
	}
	return ""
}

func isAlgorithmMismatch(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no common algorithm") ||
		strings.Contains(s, "handshake failed: eof") ||
		strings.Contains(s, "no matching")
}

// isTransientRateLimitDrop reports whether an error looks like a transient connection
// termination caused by network device connection-limit or rate-limit exhaustion
// (e.g. JunOS "connection-limit 10" or "rate-limit 5" terminating the handshake with EOF
// or TCP reset).
func isTransientRateLimitDrop(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "handshake failed: eof") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "broken pipe")
}

// connectDevice handles the multi-stage connection logic (bastion jump host,
// learned route memory, direct connection, and neighbor auto-fallback),
// returning an authenticated *ssh.Client without running any commands.
func connectDevice(ctx context.Context, dev inventory.Device, cfg Config, pool *jumpPool) (*ssh.Client, error) {
	creds := cfg.creds(dev.CredentialGroup)
	devAuth := sshclient.AuthOptions{
		Username: creds.Username,
		Password: creds.Password,
		KeyFile:  creds.KeyFile,
	}

	var client *ssh.Client
	var err error

	jumpAddr := resolveJumpAddr(dev, cfg)
	if jumpAddr != "" {
		if pool == nil {
			pool = newJumpPool(cfg.SSH, cfg.MemStore)
			defer pool.closeAll()
		}
		// Resolve jump credentials: per-named-jump first, then global defaults.
		var jumpAuth sshclient.AuthOptions
		if dev.JumpHost != "" && cfg.CredStore != nil {
			jc := cfg.CredStore.ForJumpHost(dev.JumpHost)
			jumpAuth = sshclient.AuthOptions{
				Username: jc.Username,
				Password: jc.Password,
				KeyFile:  jc.KeyFile,
			}
		} else if cfg.CredStore != nil {
			jd := cfg.CredStore.JumpDefaults()
			jumpAuth = sshclient.AuthOptions{
				Username: jd.Username,
				Password: jd.Password,
				KeyFile:  jd.KeyFile,
			}
		}
		// Final fallback: use device credentials for the bastion too.
		if jumpAuth.Username == "" {
			jumpAuth = devAuth
		}

		jumpClient, err := pool.get(ctx, jumpAddr, jumpAuth)
		if err != nil {
			return nil, errcat.Classify(dev.Hostname, "connect-jump", fmt.Errorf("bastion %s: %w", jumpAddr, err))
		}
		client, err = sshclient.ConnectViaJumpWithAuth(ctx, jumpClient, dev.Address, dev.Port, devAuth, cfg.SSH)
		if err != nil {
			if !cfg.SSH.LegacyAlgorithms && isAlgorithmMismatch(err) {
				legacySSH := cfg.SSH
				legacySSH.LegacyAlgorithms = true
				if retryClient, retryErr := sshclient.ConnectViaJumpWithAuth(ctx, jumpClient, dev.Address, dev.Port, devAuth, legacySSH); retryErr == nil {
					client = retryClient
					err = nil
				}
			}
		}
		if err != nil {
			return nil, errcat.Classify(dev.Hostname, "connect", err)
		}
	} else {
		// 2. Learned route memory (persisted from earlier runs)
		connectedViaMemory := false
		if cfg.JumpEnabled && pool != nil && pool.memStore != nil {
			if rec, ok := pool.memStore.Get(dev.Address); ok {
				for _, cand := range cfg.AutoFallbackDevices {
					candEndpoint := cand.Endpoint()
					if candEndpoint == rec.JumpEndpoint || cand.Address == rec.JumpEndpoint || cand.Hostname == rec.JumpHost {
						candCreds := cfg.creds(cand.CredentialGroup)
						candAuth := sshclient.AuthOptions{
							Username: candCreds.Username,
							Password: candCreds.Password,
							KeyFile:  candCreds.KeyFile,
						}
						jumpClient, err := pool.get(ctx, candEndpoint, candAuth)
						if err == nil {
							cl, err := sshclient.ConnectViaJumpWithAuth(ctx, jumpClient, dev.Address, dev.Port, devAuth, cfg.SSH)
							if err != nil && !cfg.SSH.LegacyAlgorithms && isAlgorithmMismatch(err) {
								legacySSH := cfg.SSH
								legacySSH.LegacyAlgorithms = true
								cl, err = sshclient.ConnectViaJumpWithAuth(ctx, jumpClient, dev.Address, dev.Port, devAuth, legacySSH)
							}
							if err == nil {
								client = cl
								connectedViaMemory = true
								_ = pool.memStore.Record(dev.Hostname, dev.Address, cand.Hostname, candEndpoint, "memory-active")
								break
							}
						}
					}
				}
			}
		}

		// 3. Direct connection
		if !connectedViaMemory {
			directOpts := cfg.SSH
			if cfg.JumpEnabled && len(cfg.AutoFallbackDevices) > 0 && directOpts.ConnectTimeout > 3500*time.Millisecond {
				directOpts.ConnectTimeout = 3500 * time.Millisecond
			}

			client, err = sshclient.ConnectWithAuth(ctx, dev.Address, dev.Port, devAuth, directOpts)
			if err != nil {
				if !directOpts.LegacyAlgorithms && isAlgorithmMismatch(err) {
					legacySSH := directOpts
					legacySSH.LegacyAlgorithms = true
					if retryClient, retryErr := sshclient.ConnectWithAuth(ctx, dev.Address, dev.Port, devAuth, legacySSH); retryErr == nil {
						client = retryClient
						err = nil
					}
				}
			}
			if err != nil {
				if pool != nil {
					pool.markBadBastion(dev.Address)
				}

				// 4. Auto-fallback to neighbor device
				if cfg.JumpEnabled && pool != nil && len(cfg.AutoFallbackDevices) > 0 && ctx.Err() == nil {
					targetIP := net.ParseIP(dev.Address)
					for _, cand := range cfg.AutoFallbackDevices {
						if cand.Address == dev.Address || cand.Hostname == dev.Hostname || pool.isBadBastion(cand.Address) {
							continue
						}
						candIP := net.ParseIP(cand.Address)
						sameSubnet := targetIP != nil && candIP != nil && targetIP.To4() != nil && candIP.To4() != nil &&
							targetIP.To4()[0] == candIP.To4()[0] && targetIP.To4()[1] == candIP.To4()[1] && targetIP.To4()[2] == candIP.To4()[2]

						if !sameSubnet && len(cfg.AutoFallbackDevices) > 5 {
							continue
						}

						candEndpoint := cand.Endpoint()
						candCreds := cfg.creds(cand.CredentialGroup)
						candAuth := sshclient.AuthOptions{
							Username: candCreds.Username,
							Password: candCreds.Password,
							KeyFile:  candCreds.KeyFile,
						}
						jumpOpts := cfg.SSH
						if jumpOpts.ConnectTimeout > 2500*time.Millisecond {
							jumpOpts.ConnectTimeout = 2500 * time.Millisecond
						}
						jumpClient, jErr := pool.get(ctx, candEndpoint, candAuth)
						if jErr != nil {
							continue
						}
						cl, cErr := sshclient.ConnectViaJumpWithAuth(ctx, jumpClient, dev.Address, dev.Port, devAuth, cfg.SSH)
						if cErr == nil {
							client = cl
							if pool.memStore != nil {
								_ = pool.memStore.Record(dev.Hostname, dev.Address, cand.Hostname, candEndpoint, "auto-fallback")
							}
							err = nil
							break
						}
					}
				}

				if err != nil {
					return nil, errcat.Classify(dev.Hostname, "connect", err)
				}
			}
		}
	}
	if err != nil {
		return nil, errcat.Classify(dev.Hostname, "connect", err)
	}

	return client, nil
}

func fetchConfig(ctx context.Context, dev inventory.Device, profile vendor.Profile, cfg Config, pool *jumpPool) (string, error) {
	client, err := connectDevice(ctx, dev, cfg, pool)
	if err != nil {
		return "", err
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

	if err := profile.ValidateOutput(output); err != nil {
		return "", errcat.Classify(dev.Hostname, "validate", err)
	}

	return output, nil
}

// save writes the captured config to <outputDir>/<runName>/<vendor>/<hostname><ext>.
func save(dev inventory.Device, profile vendor.Profile, content string, cfg Config) (string, error) {
	vendorDir := filepath.Join(cfg.OutputDir, cfg.RunName, dev.Vendor)
	if err := os.MkdirAll(vendorDir, 0o700); err != nil {
		return "", fmt.Errorf("creating vendor dir: %w", err)
	}

	filename := sanitize(dev.Hostname) + profile.FileExtension
	path := filepath.Join(vendorDir, filename)

	if _, err := os.Stat(path); err == nil {
		// Collision fallback
		filename = fmt.Sprintf("%s_%s%s", sanitize(dev.Hostname), sanitize(dev.Address), profile.FileExtension)
		path = filepath.Join(vendorDir, filename)
	}

	if err := WriteFileAtomic(path, []byte(content), 0o600); err != nil {
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
				// Also clean up accompanying .diff file if present
				diffPath := strings.TrimSuffix(f.path, ext) + ".diff"
				_ = os.Remove(diffPath)

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
