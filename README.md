# netbackup

A single-binary, zero-dependency tool that backs up running
configurations from a list of network devices over SSH. Built for
air-gapped management networks: it needs no internet access to **run**,
and — because all third-party source is vendored into this package —
no internet access to **build** either. There is nothing to download at
any stage.

## Quick Start (Universal Deployment)

No matter what device or OS you are using (Linux, macOS, Windows, x86_64, ARM64), you don't need to remember binary names or build steps.

### Option 1: Universal Auto-Detect Script (Linux / macOS / Windows)
Simply execute `./run` (or `.\scripts\run.bat` on native Windows CMD). It automatically detects your host OS and CPU architecture and launches the correct pre-built binary:

```bash
make init          # 1. Initialize .env and inventory.csv
./run              # 2. Automatically launches the matching pre-built binary
```

### Viewing Backups (`./show-backup`)
Quickly view or locate the latest configuration backup or diff for any node:

```bash
./show-backup <node-name>     # View latest configuration in terminal pager
./show-backup GW-01           # Prompts interactive selection if multiple match
./show-backup -d <node-name>  # View latest diff
./show-backup -p <node-name>  # Print exact file path only
./show-backup -l              # List all available nodes with backups
```

### Option 2: Download from GitHub Releases
All builds are automatically compiled and published to GitHub on every commit:
1. Go to the [Releases](https://github.com/duc-mt/netbackup/releases) page on GitHub.
2. Under **Latest Build**, download the single binary for your target OS (`.exe` for Windows, `linux-amd64` for Linux).
3. Bring only this binary to the target machine.
4. Run `netbackup.exe -init` (or `./netbackup-linux-amd64 -init`) to generate sample `inventory.csv` and `.env` template files instantly!

### Option 3: Standard Make / Direct Binary
- **Initialize config:** `make init`
- **Build (offline):** `make build` (or `make cross` for all platforms)
- **Run direct binary:** `./bin/netbackup`

## Fastest path: just download and run the binary

Download the matching single file from [GitHub Releases](https://github.com/duc-mt/netbackup/releases):

| File | Platform |
|---|---|
| `netbackup-linux-amd64` | Linux, x86-64 (most servers/jump hosts) |
| `netbackup-linux-arm64` | Linux, ARM64 |
| `netbackup-windows-amd64.exe` | Windows, x86-64 |
| `netbackup-darwin-amd64` | macOS, Intel |
| `netbackup-darwin-arm64` | macOS, Apple Silicon |

Copy the single executable onto any machine (online or air-gapped). Run `netbackup -init` to generate template configuration files, customize them, and run. That's the entire deployment. You only need to build if you are modifying source code locally.

## Key Features

1. **Air-gapped & Zero-dependency**: Fully offline execution and build.
2. **Sensitive Data Redaction**: Automatic masking of passwords, secrets,
   SNMP community strings, and pre-shared keys before saving to disk.
3. **Change Detection**: Detects if configuration has changed since the
   latest backup; skips saving redundant duplicate files by default.
4. **Backup Retention & Rotation**: Automatically prunes older backups per
   device based on count or age.
5. **Multi-Site & Per-Group Credentials**: Resolves per-site or per-vendor
   credentials from environment variables with graceful fallback to defaults.
6. **Thread-Safe & Fault-Isolated**: Bounded worker pool concurrency with
   per-goroutine panic recovery.
7. **Smart / Semantic Diff**: Ignores volatile lines (clock stamps, "last
   changed" comments, uptime banners) so noise alone never counts as a change.
8. **Risk Classification & Security Alerts**: Tags every change CRITICAL,
   WARNING or INFO (AAA, ACLs, SNMP, SSH, routing, VLANs, ...).
9. **Golden-Config Drift Detection**: Checks each device against per-vendor
   baselines and reports missing, mismatched or forbidden lines.
10. **HTML & Markdown Reports**: Self-contained run report and a running
    changelog with collapsible, colored diffs.

## Project layout

```
netbackup/
├── go.mod
├── Makefile
├── cmd/
│   └── netbackup/
│       └── main.go                    # CLI flags + orchestration
├── internal/
│   ├── inventory/inventory.go         # RFC CSV device list parser (with credential_group)
│   ├── credentials/credentials.go     # multi-group .env / env-var / secure prompt store
│   ├── redact/redact.go               # sensitive credential & secret masking
│   ├── vendor/vendor.go               # per-platform backup command map
│   ├── sshclient/sshclient.go         # thread-safe SSH connect + run, hard timeouts
│   ├── backup/backup.go               # worker pool, change detection, and retention pruning
│   ├── backup/filter.go               # volatile-line filter (semantic diff)
│   ├── backup/risk.go                 # CRITICAL / WARNING / INFO change classifier
│   ├── compliance/compliance.go       # golden-config baselines and drift detection
│   ├── report/                        # result table, HTML report, Markdown changelog
│   ├── errcat/errcat.go               # error categorization
│   └── logging/logging.go             # console + file logging, run summary
├── vendor/                            # vendored dependencies (x/crypto, x/term, x/sys)
├── bin/                               # pre-built, ready-to-run binaries
└── examples/
    ├── inventory.csv                  # sample inventory
    ├── env.example                    # sample credentials
    ├── golden/                        # sample golden baselines (<vendor>.golden)
    └── volatile-patterns.txt          # sample extra volatile-line patterns
```

## Build from source (fully offline — no internet at any step)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags="-s -w" -o netbackup-linux-amd64 ./cmd/netbackup
```

Go automatically builds from `vendor/` whenever that directory is present
and consistent with `go.mod` (true here) — no flags needed, and no
`go.sum` is required either.

## Run

### 1. Initialize configuration

Fastest way (with the single binary, zero dependencies):
```bash
netbackup -init
```
*(Or if using the repository with Make: `make init`)*

This automatically creates `.env` (with `chmod 600`) and `inventory.csv` without overwriting existing files.

Or manually:
```bash
cp examples/inventory.csv inventory.csv   # edit with your real devices
cp examples/env.example .env             # edit with real credentials
chmod 600 .env
```

### 2. Execute backup

```bash
./bin/netbackup-linux-amd64 \
  -inventory inventory.csv \
  -out backups \
  -log-dir logs \
  -env-file .env \
  -concurrency 5 \
  -connect-timeout 10s \
  -command-timeout 30s \
  -redact=true \
  -retention-count 10 \
  -retention-days 30
```

### CLI Flags

| Flag | Default | Description |
|---|---|---|
| `-init` | `false` | Generate sample `inventory.csv` and `.env` template in current directory and exit |
| `-inventory` | `inventory.csv` | Path to CSV inventory file |
| `-out` | `backups` | Directory to write backup configuration files into |
| `-log-dir` | `logs` | Directory to write run logs into |
| `-env-file` | `.env` | Path to optional `.env` file |
| `-concurrency` | `5` | Maximum number of concurrent SSH device connections |
| `-connect-timeout` | `10s` | TCP dial + SSH handshake timeout per device |
| `-command-timeout` | `30s` | Timeout for command execution per device |
| `-redact` | `true` | Redact passwords, pre-shared keys, and SNMP communities |
| `-save-unchanged` | `false` | Save new file even if configuration is identical to previous backup |
| `-retention-count`| `0` | Max backups to keep per device (`0` = disabled) |
| `-retention-days` | `0` | Delete backups older than N days (`0` = disabled) |
| `-result-table`   | `true`  | Display colorized summary table on terminal (`NO_COLOR` to disable) |
| `-ignore-volatile`| `true`  | Ignore volatile lines (timestamps, clock stamps, ...) when detecting changes |
| `-volatile-patterns` | `""` | File of extra volatile-line regexes, one per line (`#` = comment) |
| `-golden-dir`     | `""`    | Directory of `<vendor>.golden` baselines (+ optional `common.golden`); enables compliance checks |
| `-compliance-rules` | `""`  | Single rules file with optional `[vendor]` sections; can be combined with `-golden-dir` |
| `-html-report`    | `""`    | Write a standalone HTML run report to this path |
| `-md-report`      | `""`    | Write/update a Markdown changelog at this path (newest run first) |
| `-key-file`       | `""`    | Path to SSH private key file for device authentication (overrides `NETBACKUP_KEY_FILE`) |
| `-retries`        | `0`     | Number of retry attempts per device upon failure (`0` = no retry) |
| `-retry-delay`    | `2s`    | Delay between retries per device |
| `-jump`           | `true`  | Enable bastion/jump host routing (`-jump=false` connects directly) |
| `-jump-host`      | `""`    | Global default bastion `host[:port]` |
| `-jump-user`      | `""`    | SSH username for global bastion (overrides `NETBACKUP_JUMP_USERNAME`) |
| `-jump-password`  | `""`    | SSH password for global bastion (overrides `NETBACKUP_JUMP_PASSWORD`) |
| `-jump-key-file`  | `""`    | SSH private key file for global bastion (overrides `NETBACKUP_JUMP_KEY_FILE`) |

### Run Summary Table

When `-result-table=true` (or when running via `./run`), netbackup outputs a summary table featuring rounded box borders (`╭ ╮ ╰ ╯`), colorized status badges, and right-aligned metrics:

```text
╭─────────────────────────┬──────────────────┬───────────────┬───────────┬──────────┬──────────────────────────────────────────────────╮
│ HOSTNAME/IP             │ PLATFORM         │ STATUS        │ FILE SIZE │ DURATION │ ERROR                                            │
├─────────────────────────┼──────────────────┼───────────────┼───────────┼──────────┼──────────────────────────────────────────────────┤
│ core-sw01 (10.0.1.1)    │ cisco_ios        │ ✔ SUCCESS     │ 44.82 KiB │    3.42s │ -                                                │
│ edge-rtr02 (10.0.2.5)   │ juniper_junos    │ ✔ UNCHANGED   │         - │    2.10s │ -                                                │
│ fw-dc01 (10.0.3.10)     │ fortinet_fortios │ ✖ AUTH FAILED │         - │    1.05s │ ssh: unable to authenticate, attempted methods … │
│ sw-branch09 (10.0.9.2)  │ arista_eos       │ ⏱ TIMEOUT     │         - │   30.00s │ command timed out after 30s                      │
│ sw-branch14 (10.0.14.3) │ cisco_ios        │ ✖ FAILED      │         - │    0.87s │ no route to host                                 │
╰─────────────────────────┴──────────────────┴───────────────┴───────────┴──────────┴──────────────────────────────────────────────────╯

  ✔ Succeeded: 2  │  ✖ Failed: 3  │  (total 5)
```

Exit codes: `0` = every device succeeded, `1` = fatal startup error
(bad inventory path, invalid credentials), `2` = ran to completion but one
or more devices failed (check logs for details).

### Smart (semantic) diff

Network OSes stamp their output with values that change on every poll, which
would otherwise make every device look "changed". With
`-ignore-volatile=true` (the default) such lines are dropped from **both**
sides before the previous and the new configuration are compared:

| Platform | Ignored lines |
|---|---|
| Cisco IOS / Arista / Huawei / Aruba / Ruijie | `! Last configuration change at ...`, `! NVRAM config last updated at ...`, `! No configuration change since last restart`, `! Time: ...`, `Current configuration : N bytes`, `Building configuration...`, `ntp clock-period N`, Huawei `!Last configuration was updated/saved at ...` |
| Junos | `## Last changed: ...`, `## Last commit: ...` |
| VyOS | `// Warning: Do not remove ...`, `// vyos-config-version ...`, `// Release version ...` (and the `/* */` forms) |
| VyOS / pfSense / Gaia | `# Generated/Exported/Date/Timestamp/Uptime ...` comment banners |

* A device whose only differences are volatile lines is reported as
  `UNCHANGED` (and logged as `UNCHANGED (volatile lines only)`); no new file
  is written unless `-save-unchanged` is set.
* The file saved to disk is always the original configuration. The filter is
  used only for the comparison, and the `.diff` file shows the filtered diff.
* Add site-specific noise with `-volatile-patterns <file>` (see
  `examples/volatile-patterns.txt`). A bad pattern is reported and skipped.
* `-ignore-volatile=false` restores the previous behaviour.

### Risk classification

Every added (`+`) or removed (`-`) diff line is classified; the highest level
found is attached to the device result:

| Level | Badge | Triggers |
|---|---|---|
| **CRITICAL** | `[CRITICAL: AAA]`, `[CRITICAL: ACL]`, ... | AAA / TACACS / RADIUS, local users and credentials, SNMP communities, ACLs, firewall rules, SSH port / ciphers |
| **WARNING** | `[ROUTING]`, `[VLAN]`, `[INTERFACE]` | BGP neighbors / AS, OSPF, default and static routes, VLANs, interface `shutdown` / `no shutdown` / `disable` |
| **INFO** | `[INFO]` | NTP, description / remark, hostname, banner / MOTD |

* The badge appears next to the diff counts in the result table
  (`+2/-1 [CRITICAL: ACL]`), in the run summary (`RISK=CRITICAL(ACL)`), and
  as an inline `<== CRITICAL: ACL` marker on the offending diff lines.
  CRITICAL and WARNING lines are logged at `WARN` level, and the run summary
  ends with a **Risk alerts** block.
* Description / remark lines never escalate, even if the text mentions
  "radius" or "ospf".
* Rules live in `internal/backup/risk.go` (`builtinRiskSpecs`, first match
  wins); add a row there to teach netbackup a new pattern.
* **Limitation:** with `-redact=true` (the default) secrets are stored as
  `<REDACTED>`, so rotating a password or SNMP community produces no diff at
  all. Creating, deleting or re-parameterising a user / community is still
  detected.

### Golden-config compliance

Provide baselines with `-golden-dir <dir>` and/or `-compliance-rules <file>`.
A directory holds one `<vendor>.golden` file per platform (the vendor key or
any alias: `vyos.golden`, `cisco.golden`, `junos.golden`, ...) plus an
optional `common.golden` that applies to every device. A rules file uses
`[vendor]` section headers instead (lines before the first header are
common). See `examples/golden/`.

```text
# A comment ("!" and "//" also start one). Comments must be on their own line.

# Required line (exact match):
set service ntp server time1.vyos.net

# Required: at least one line matches this regex:
re: ^set system syslog host\s+10\.

# Forbidden line:
not: set service telnet

# Forbidden: no line may match this regex:
not-re: community\s+public\b
```

Exact matching ignores indentation, repeated whitespace and quote
characters. Matching is line based and context free.

* Drifts are reported per device as `missing`, `mismatch` (a line with the
  same key but another value exists, e.g. `set service ssh port 2222` when
  `... port 22` is required) or `forbidden`.
* The result table gains a `COMPLIANCE` column (`✔ COMPLIANT` /
  `✖ 2 DRIFTS` / `-` when no baseline applies) and a compliance rate line.
* Devices are checked on every run, including unchanged ones. A baseline that
  cannot be loaded only disables the check (warning in the log); it never
  stops the backup.

### HTML & Markdown reports

```bash
./netbackup ... -html-report 'backups/{run}/report.html' -md-report reports/CHANGELOG.md
```

* **HTML** is a single self-contained file (inline CSS, no scripts, no
  external resources, light/dark and mobile friendly): summary cards (total,
  succeeded, unchanged, changed, failed, compliance rate), a result table with
  status / risk / compliance badges, collapsible green/red diffs for changed
  devices (CRITICAL ones start expanded) and a compliance-drift section. It is
  a snapshot of the run and is overwritten each time.
* **Markdown** (GitHub-flavored) holds a summary table, a result table and
  fenced `diff` blocks. When the file already exists it is a running
  changelog: the new run is inserted at the top and earlier runs are kept (a
  file that was not created by netbackup is appended to, never overwritten).
  Use a `{run}` or `{timestamp}` placeholder for one file per run.
* Path placeholders: `{run}` (e.g. `backup-2026-10-12-0757`), `{timestamp}`
  (`20261012-075730`) and `{date}` (`2026-10-12`); the `<run>` / `<timestamp>`
  spellings also work.
* A report that cannot be written is logged as a warning; it never changes
  the exit code.

## Inventory & Multi-Site Credentials

```csv
hostname,address,port,vendor,credential_group,jump_host
core-sw-01,10.10.1.1,22,cisco_ios,site_hcm,
dc-fw-01,10.20.1.1,22,fortigate,site_dc,bastion-dc
edge-rtr-01,10.30.1.1,22,juniper_junos,default,mgmt-gw.corp.com:22
```

- `port` defaults to `22`.
- `vendor` defaults to `generic` (`show running-config`). Supported vendor keys:
  `cisco_ios`, `huawei_vrp`, `juniper_junos`, `aruba`, `ruijie`, `fortigate`,
  `checkpoint_gaia`, `paloalto`, `pfsense`, `sophos`, `vyos`, `arista`, `generic`.
- `credential_group` defaults to `default`. If specified as `site_hcm`, the
  tool looks for `NETBACKUP_SITE_HCM_USERNAME` and `NETBACKUP_SITE_HCM_PASSWORD`,
  falling back to `NETBACKUP_USERNAME` / `NETBACKUP_PASSWORD` if not set.
- `jump_host` (optional):
  - Blank: connects directly (or uses global `-jump-host` / `NETBACKUP_JUMP_HOST` if set).
  - Named bastion (e.g. `bastion-dc`): resolves from `NETBACKUP_JUMP_BASTION_DC_HOST`, `_USERNAME`, `_PASSWORD`.
  - Direct host/port (e.g. `mgmt-gw.corp.com:22`): dials that address as the bastion.
  - Disable all jump routing on demand with `-jump=false`.


## Development, Maintenance & Expansion

### Useful Developer One-Liners

- **Run all unit tests**:
  ```bash
  go test ./...
  ```
- **Test & Build native binary**:
  ```bash
  go test ./... && CGO_ENABLED=0 go build -ldflags="-s -w" -o netbackup .
  ```
- **Test & Rebuild ALL cross-platform binaries into `bin/`**:
  ```bash
  go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/netbackup-linux-amd64 . && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/netbackup-linux-arm64 . && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/netbackup-windows-amd64.exe . && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/netbackup-darwin-amd64 . && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/netbackup-darwin-arm64 .
  ```

### Maintaining & Expanding netbackup

1. **Adding a New Vendor Platform**:
   - Add an entry to `Profiles` in `internal/vendor/vendor.go`.
   - Specify `Name`, `Interactive` mode (`true` for console menus, `false` for non-interactive SSH exec), `SetupCommands`, `BackupCommand`, and `FileExtension`.
   - Add unit tests in `internal/vendor/vendor_test.go`.

2. **Adding Redaction Rules**:
   - Add regex rules to `commonPatterns` in `internal/redact/redact.go` to mask vendor-specific passwords, pre-shared keys, or secrets.
   - Add unit test cases in `internal/redact/redact_test.go`.

3. **Updating Vendored Dependencies**:
   - When online, run `go mod tidy && go mod vendor` to update offline dependencies in `vendor/`.

## Security notes

- No credentials or sensitive network secrets are committed or logged.
- Backups and logs are excluded from version control (`.gitignore`).
- Redaction automatically masks passwords, pre-shared keys, private keys,
  and SNMP community strings before configs are written to disk.
- Passing `-username`/`-password` as CLI flags is intentionally not supported
  to prevent credentials from appearing in `ps` output or shell history.
