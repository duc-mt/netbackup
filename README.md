# netbackup

A single-binary, zero-dependency tool that backs up running
configurations from a list of network devices over SSH. Built for
air-gapped management networks: it needs no internet access to **run**,
and — because all third-party source is vendored into this package —
no internet access to **build** either. There is nothing to download at
any stage.

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
├── main.go                        # CLI flags + orchestration
├── internal/
│   ├── inventory/inventory.go     # RFC CSV device list parser (with credential_group)
│   ├── credentials/credentials.go # multi-group .env / env-var / secure prompt store
│   ├── redact/redact.go           # sensitive credential & secret masking
│   ├── vendor/vendor.go           # per-platform backup command map
│   ├── sshclient/sshclient.go     # thread-safe SSH connect + run, hard timeouts
│   ├── backup/backup.go           # worker pool, change detection, and retention pruning
│   ├── errcat/errcat.go           # error categorization
│   └── logging/logging.go         # console + file logging, run summary
├── vendor/                        # vendored dependencies (x/crypto, x/term, x/sys)
├── bin/                           # pre-built, ready-to-run binaries
├── inventory.example.csv          # copy to inventory.csv and edit
└── .env.example                   # copy to .env and edit
```

## Build from source (fully offline — no internet at any step)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o netbackup-linux-amd64 .
```

Go automatically builds from `vendor/` whenever that directory is present
and consistent with `go.mod` (true here) — no flags needed, and no
`go.sum` is required either.

## Run

```bash
cp inventory.example.csv inventory.csv   # edit with your real devices
cp .env.example .env                     # edit with real credentials
chmod 600 .env

./netbackup-linux-amd64 \
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
  `checkpoint_gaia`, `pfsense`, `generic`.
- `credential_group` defaults to `default`. If specified as `site_hcm`, the
  tool looks for `NETBACKUP_SITE_HCM_USERNAME` and `NETBACKUP_SITE_HCM_PASSWORD`,
  falling back to `NETBACKUP_USERNAME` / `NETBACKUP_PASSWORD` if not set.

## Security notes

- No credentials or sensitive network secrets are committed or logged.
- Backups and logs are excluded from version control (`.gitignore`).
- Redaction automatically masks passwords, pre-shared keys, private keys,
  and SNMP community strings before configs are written to disk.
- Passing `-username`/`-password` as CLI flags is intentionally not supported
  to prevent credentials from appearing in `ps` output or shell history.
