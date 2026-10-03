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

## How it works

1. Read a device list from a CSV inventory file.
2. For each device, open an SSH connection (bounded by a connect timeout),
   run the platform-appropriate "show config" command (bounded by a
   command timeout), and save the output to a timestamped file.
3. One device's failure never stops the run — every failure is caught,
   categorized, logged, and the tool moves on to the next device.
4. At the end, print a summary: how many succeeded, how many failed, and
   why.

## Project layout

```
netbackup/
├── go.mod
├── main.go                        # CLI flags + orchestration
├── internal/
│   ├── inventory/inventory.go     # CSV device list parser
│   ├── credentials/credentials.go # env-file / env-var / secure prompt
│   ├── vendor/vendor.go           # per-platform backup command map
│   ├── sshclient/sshclient.go     # SSH connect + run, with hard timeouts
│   ├── backup/backup.go           # per-device orchestration + worker pool
│   ├── errcat/errcat.go           # error categorization
│   └── logging/logging.go         # console + file logging, run summary
├── vendor/                        # full source of the 3 dependencies used
│                                   # (golang.org/x/crypto, x/term, x/sys) —
│                                   # already included, nothing to fetch
├── bin/                           # pre-built, ready-to-run binaries
├── inventory.example.csv          # copy to inventory.csv and edit
└── .env.example                   # copy to .env and edit
```

## Build from source (fully offline — no internet at any step)

This only matters if you change the code or need a platform not already
in `bin/`. Requires a Go 1.22+ toolchain on *some* machine — Go itself
still needs to be installed once, same as any compiler, but nothing it
does from here on touches the network. The `vendor/` directory already
contains the complete source of the only three packages this project
depends on, so the build never contacts the internet:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o netbackup-linux-amd64 .
```

Go automatically builds from `vendor/` whenever that directory is present
and consistent with `go.mod` (true here) — no flags needed, and no
`go.sum` is required either, since vendored builds don't consult a
checksum database at all. This was verified by building with
`GOPROXY=off` and an empty module cache: the build still succeeds,
proving no network call occurs.

Cross-compile for another target the same way, just by changing
`GOOS`/`GOARCH` (`linux/arm64`, `windows/amd64`, `darwin/amd64`,
`darwin/arm64`, etc.) — Go cross-compiles natively, so you don't need
the target OS to build for it.

If you ever want to re-fetch the vendored dependencies yourself (e.g.
to update a version) you'd need internet for that one `go mod vendor`
step — but that's a maintenance action on your build machine, not
something this tool or its normal build ever requires.

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
  -command-timeout 30s
```

If `.env` is absent and the `NETBACKUP_USERNAME` / `NETBACKUP_PASSWORD`
environment variables aren't set either, the tool prompts for them
interactively (password input is not echoed to the terminal).

Exit codes: `0` = every device succeeded, `1` = a fatal startup error
(bad inventory path, can't resolve credentials), `2` = ran to completion
but one or more devices failed (check the log for which ones and why).

## Inventory file format

```
hostname,address,port,vendor
core-sw-01,10.10.1.1,22,cisco_ios
```

`port` defaults to `22`, `vendor` defaults to `generic`
(`show running-config`). Supported vendor keys today: `cisco_ios`,
`huawei_vrp`, `juniper_junos`, `aruba`, `ruijie`, `fortigate`,
`checkpoint_gaia`, `pfsense`. Adding a new platform is a single new entry
in `internal/vendor/vendor.go` — nothing else in the codebase needs to
change.

## Things worth validating against your real devices before rollout

- **Exact CLI commands and output format vary by firmware version** —
  the commands in `vendor.go` are the standard ones for each platform,
  but as with any multi-vendor CLI automation, it's worth a validation
  pass against your actual firmware/software versions before relying on
  this for production backups, the same way any new per-vendor automation
  does.
- **pfSense** needs its SSH account configured with real shell access
  (System > User Manager), not the default restricted console menu, or
  the tool will capture the menu text instead of `/cf/conf/config.xml`.
- **Host key verification**: `sshclient.go` currently uses
  `ssh.InsecureIgnoreHostKey()` for simplicity. For a hardened rollout,
  replace this with a callback that checks a `known_hosts` file you
  maintain for the environment, so the tool fails closed against a
  spoofed device rather than silently trusting any host key.
- **Shared credentials**: as written, one username/password is used for
  every device. If your devices use per-site or per-vendor service
  accounts, extend `inventory.Device` with an optional credential-profile
  column and look it up in a small map at startup — the SSH and backup
  layers already take credentials as parameters, so this is a localized
  change.

## Security notes

- No credentials are ever hardcoded or logged.
- Credential resolution order: `.env` file → environment variables →
  interactive prompt.
- Passing `-username`/`-password` as CLI flags was intentionally **not**
  implemented, since flags are visible in `ps` output and shell history
  on multi-user systems — use the `.env` file or environment variables
  instead.
