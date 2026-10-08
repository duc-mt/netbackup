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
Simply execute `./run.sh` (or `run.bat` on Windows). It automatically detects your host OS and CPU architecture and launches the correct pre-built binary:

```bash
make init          # 1. Initialize .env and inventory.csv
./run.sh           # 2. Automatically launches the matching pre-built binary
```

### Option 2: Docker / Docker Compose
If you prefer running in a container:

```bash
docker compose up  # Builds container (if needed) and executes backup
```

### Option 3: Standard Make / Direct Binary
- **Initialize config:** `make init`
- **Build (offline):** `make build`
- **Run direct binary:** `./bin/netbackup-linux-amd64` (or matching OS in `bin/`)

## Fastest path: just run the binary

`bin/` already contains ready-to-run, statically linked binaries. No Go
toolchain, no install step, no internet, on any OS:

| File | Platform |
|---|---|
| `bin/netbackup-linux-amd64` | Linux, x86-64 (most servers/jump hosts) |
| `bin/netbackup-linux-arm64` | Linux, ARM64 |
| `bin/netbackup-windows-amd64.exe` | Windows, x86-64 |
| `bin/netbackup-darwin-amd64` | macOS, Intel |
| `bin/netbackup-darwin-arm64` | macOS, Apple Silicon |

Copy the one matching file, plus `inventory.csv` and `.env`, onto the
air-gapped machine and run it (see "Run" below). That's the entire
deployment. You only need the "Build" section if you change the source
or need a platform not listed above.

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
│   ├── errcat/errcat.go               # error categorization
│   └── logging/logging.go             # console + file logging, run summary
├── vendor/                            # vendored dependencies (x/crypto, x/term, x/sys)
├── bin/                               # pre-built, ready-to-run binaries
└── examples/
    ├── inventory.csv                  # sample inventory
    └── env.example                    # sample credentials
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

Fastest way:
```bash
make init
```

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

Exit codes: `0` = every device succeeded, `1` = fatal startup error
(bad inventory path, invalid credentials), `2` = ran to completion but one
or more devices failed (check logs for details).

## Inventory & Multi-Site Credentials

```csv
hostname,address,port,vendor,credential_group
core-sw-01,10.10.1.1,22,cisco_ios,site_hcm
dc-fw-01,10.20.1.1,22,fortigate,site_dc
edge-rtr-01,10.30.1.1,22,juniper_junos,default
```

- `port` defaults to `22`.
- `vendor` defaults to `generic` (`show running-config`). Supported vendor keys:
  `cisco_ios`, `huawei_vrp`, `juniper_junos`, `aruba`, `ruijie`, `fortigate`,
  `checkpoint_gaia`, `pfsense`, `vyos`, `arista`, `generic`.
- `credential_group` defaults to `default`. If specified as `site_hcm`, the
  tool looks for `NETBACKUP_SITE_HCM_USERNAME` and `NETBACKUP_SITE_HCM_PASSWORD`,
  falling back to `NETBACKUP_USERNAME` / `NETBACKUP_PASSWORD` if not set.

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
