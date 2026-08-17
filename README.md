# DockVault

**DockVault** discovers Docker volumes, containers, and host paths, infers
the service running on each one, and backs it up — and restores it —
to Google Drive via [`rclone`](https://rclone.org/). It's a Go rewrite of an
original Bash/Bashly CLI, built with a plugin architecture for a future
security-scanning add-on (DockSec).

![Go Version](https://img.shields.io/badge/go-1.21%2B-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)
![Status](https://img.shields.io/badge/status-feature--complete-brightgreen)

> **Status:** Feature-complete. Every command (`setup`, `scan`, `generate`,
> `backup`, `restore`, `list`, `tree`, `schedule`) is fully implemented and
> validated against real Docker containers, not just mocked unit tests.
> See [Implementation status](#implementation-status) for details.
>
> DockVault executes backups/restores directly in Go — it does **not**
> generate `backup.sh`/`restore.sh` scripts for cron/systemd to run.
> Scheduling invokes the `dockvault` binary itself.

## Table of contents

- [What it does](#what-it-does)
- [Features](#features)
- [Requirements](#requirements)
- [Installation](#installation)
  - [Windows (PowerShell + Go)](#windows-powershell--go)
  - [Linux / macOS](#linux--macos)
- [Quick start](#quick-start)
- [Command reference](#command-reference)
- [Building from source](#building-from-source)
- [Configuration](#configuration)
- [Project structure](#project-structure)
- [Architecture notes](#architecture-notes)
- [Development / testing](#development--testing)
- [Troubleshooting](#troubleshooting)
- [Known limitations](#known-limitations)
- [License](#license)

## What it does

Point DockVault at a Docker host and it will:

1. **Discover** — named volumes, running containers, bind mounts, and
   notable host paths (`/etc/nginx`, `/etc/letsencrypt`, `.env` files under
   `/opt /srv /home /root`).
2. **Identify** — infer the service running on each volume (Postgres,
   MySQL, MongoDB, Redis, n8n, RabbitMQ, or generic) from the image name,
   confirm it from environment variables, and extract credentials into job
   metadata (never printed unmasked or logged).
3. **Recommend** — propose a backup strategy (`pg_dump`, `mysqldump`,
   `mongodump`, `BGSAVE`, `rabbitmqctl export_definitions`, volume tar,
   etc.), a job name, and a standardized Google Drive destination path for
   each volume/path it finds (see [Architecture notes](#architecture-notes)).
4. **Generate** — record each job (`job.json` metadata + a `.env`, mode
   600, if credentials or a webhook URL are involved) under
   `$DOCKVAULT_HOME`, either fully automatically (`--auto`) or one at a
   time with per-job overrides (`--interactive`).
5. **Run** — execute jobs on demand (`dockvault backup`, with bounded
   parallelism) or on a daily schedule (systemd timer on Linux, a launchd
   user agent on macOS, or printed Windows Task Scheduler instructions).
   Every run computes a SHA-256/MD5 manifest, retains a durable local copy,
   uploads the archive + manifest to Google Drive, verifies Drive received
   the exact bytes sent (hard failure on mismatch), then prunes old
   backups locally and remotely, with webhook notifications.
6. **Restore** — pick a job and a backup file (newest first), type
   `RESTORE` to confirm. DockVault re-verifies the download against its
   manifest *before* touching a database or container, and refuses if it
   doesn't match — then, only for the two types that had to stop a
   container to do it safely (Redis, n8n), asks whether to start it back
   up rather than doing that silently.

## Features

- **8 backup types**: standard volumes, PostgreSQL, MySQL, MongoDB, Redis,
  n8n, host directories, and RabbitMQ (topology export).
- **Guided setup** (`dockvault setup`) — validates a GCP service account
  JSON, wires it into an `rclone` remote, and lets you pick a Shared Drive.
- **3-2-1 backup compliance** — a durable local copy alongside every
  Google Drive upload, both checksummed end to end.
- **Integrity verification** — local SHA-256/MD5, a post-upload remote
  hash check against what Drive actually received, and a restore-time
  re-check before anything is touched.
- **Cross-platform scheduling** — systemd timer (Linux), launchd agent
  (macOS), or printed Task Scheduler instructions (Windows).
- **Safe interrupts** — every command runs under a context cancelled on
  `SIGINT`/`SIGTERM`, so Ctrl+C kills in-flight `docker`/`rclone` children
  instead of leaving them running.
- **Platform-aware credential storage** — `.env` files (mode 600) on
  Linux/macOS, Windows Credential Manager on Windows.
- **Plugin architecture** — a minimal `Plugin` interface for extensions
  like the planned DockSec security scanner.
- **No external Go dependencies** — standard library only.

## Requirements

- **Go 1.21+** (to build from source — DockVault has no external Go
  dependencies)
- **Docker**, reachable from the account running DockVault
- **[rclone](https://rclone.org/)**, configured with a remote pointing at
  Google Drive (`dockvault setup` can do this for you)
- `tar` and `gzip` (used to build archives — present by default on
  Linux/macOS; see the Windows note below)
- `make` — **optional**, only needed if you prefer the Makefile shortcuts
  over calling `go build` directly

## Installation

DockVault ships as a single static binary with no runtime dependencies
beyond `docker` and `rclone`. There are no pre-built releases yet — build
it from source with Go.

### Windows (PowerShell + Go)

1. **Check if Go is installed:**

   ```powershell
   go version
   ```

2. **Install Go if needed**, preferably with [winget](https://learn.microsoft.com/windows/package-manager/winget/):

   ```powershell
   winget install GoLang.Go
   ```

   Or with [Chocolatey](https://chocolatey.org/):

   ```powershell
   choco install golang
   ```

3. **Clone the repository:**

   ```powershell
   git clone https://github.com/kreee00/dockvault.git
   cd dockvault
   ```

4. **Build with Go directly** — `make` is optional on Windows; if you
   don't have it installed, skip straight to this:

   ```powershell
   go build -o dockvault.exe ./cmd/dockvault
   ```

5. **Run it:**

   ```powershell
   .\dockvault.exe --version
   ```

   Or run without building a binary first:

   ```powershell
   go run ./cmd/dockvault --version
   ```

> **Note:** Backup archives use `tar`/`gzip`. Recent Windows 10/11 ships a
> built-in `tar.exe`; if yours doesn't have it, install it via
> [Git for Windows](https://git-scm.com/download/win) or WSL. Windows Task
> Scheduler registration additionally needs Administrator privileges (see
> [Known limitations](#known-limitations)).

### Linux / macOS

```bash
# Check if Go is installed
go version

# Clone the repository
git clone https://github.com/kreee00/dockvault.git
cd dockvault

# Build with make (recommended — embeds version/commit/build date)
make build
./bin/dockvault --version

# ...or build with Go directly if you don't have make
go build -o dockvault ./cmd/dockvault
./dockvault --version
```

## Quick start

```bash
# Guided one-time setup: validate a GCP service account JSON, wire it into
# an rclone remote, pick a Shared Drive, set config.json defaults
./bin/dockvault setup

# See what DockVault finds on this Docker host
./bin/dockvault scan --auto

# Generate jobs from what was found (prompts for any credentials it
# couldn't auto-detect from a container's environment), then run them
./bin/dockvault generate --interactive
./bin/dockvault backup --all

# See what's backed up, locally and remotely
./bin/dockvault list --detailed
./bin/dockvault tree --detailed

# Restore a job interactively
./bin/dockvault restore
```

`scan` only needs a reachable Docker daemon. `generate` and `backup`
additionally need `rclone` configured — `backup` checks this upfront with
a clear error and remediation hint if it isn't. `setup` is the guided way
to get there instead of hand-running `rclone config`.

On Windows, replace `./bin/dockvault` with `.\dockvault.exe` (or
`.\bin\dockvault.exe` if built via `go build -o bin\dockvault.exe ...`).

## Command reference

| Command    | Purpose                                                                     |
|------------|------------------------------------------------------------------------------|
| `setup`    | Guided one-time setup: GCP service account, rclone remote, config defaults  |
| `scan`     | Discover volumes, containers, and host paths                                |
| `generate` | Create job directories (metadata + credentials) from a scan                 |
| `backup`   | Execute backup jobs (single/multiple/all, parallel)                         |
| `restore`  | Interactive restore from Google Drive                                       |
| `list`     | ASCII tree of the local `$DOCKVAULT_HOME` workspace                         |
| `tree`     | ASCII tree of the remote Google Drive folder                                |
| `schedule` | Install/check/uninstall the daily automated backup                          |

Every command supports `--help` for its own flags; global flags go
**before** the command name:

```
dockvault [global flags] <command> [command flags]
```

| Global flag        | Purpose                                                        |
|---------------------|------------------------------------------------------------------|
| `--home`            | Override `$DOCKVAULT_HOME` (default: `~/dockvault_scripts`)     |
| `--rclone-remote`   | Override the rclone remote name (default: `dockvault_backup`)   |
| `--environment`     | Override the remote path's `<environment>` segment (default: `stage`) |
| `--dry-run`         | Don't write files, don't execute backups                        |
| `--verbose`         | Verbose logging                                                 |
| `--version`         | Print version and exit                                          |

## Building from source

For contributors, or if you'd rather not use the raw `go build` commands
above, the Makefile wraps common tasks:

```make
make build          # bin/dockvault for your current GOOS/GOARCH
make build-all       # linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64
make docker-build    # cross-compile linux/amd64 inside a golang container
make test            # run all unit tests
make test-coverage   # generate an HTML coverage report
make install         # install to /usr/local/bin/dockvault
make fmt             # gofmt everything
make vet             # go vet everything
```

`make` is entirely optional — every target above is a thin wrapper around
`go build`/`go test`, so `go build -o dockvault ./cmd/dockvault` and
`go test ./...` work anywhere Go does, `make` or not.

## Configuration

`config.json` under `$DOCKVAULT_HOME` holds global defaults applied to
every job (per-job overrides are set via `generate --interactive`).
`dockvault setup` writes sensible defaults for you; the fields are:

| Field                     | Default              | Meaning                                                        |
|----------------------------|----------------------|------------------------------------------------------------------|
| `rclone_remote`            | `dockvault_backup`   | rclone remote to upload to                                      |
| `environment`               | `stage`               | `<environment>` segment of the remote path                      |
| `retention_days`            | `30`                  | Days to keep remote backups (0 disables remote pruning)         |
| `min_files_to_keep`         | `30`                  | Floor on remote backups kept regardless of age                  |
| `webhook_url`               | *(none)*              | Optional webhook for backup/restore notifications               |
| `parallel_jobs`             | `3`                   | Max concurrent backup jobs                                      |
| `local_backup_dir`          | `$DOCKVAULT_HOME/local_backups` | Where the durable local copy is kept                  |
| `local_retention_count`     | `14`                  | Local backup copies kept per job (0 disables the local copy)    |

`$DOCKVAULT_HOME` itself resolves in this order: `--home` flag >
`DOCKVAULT_HOME` environment variable > `~/dockvault_scripts`.

## Project structure

```
cmd/dockvault/        CLI entrypoint; wires SIGINT/SIGTERM -> context
internal/
  cli/                 Subcommand handlers, flag parsing, dispatch
  detector/            Volume/container/host-path discovery + service inference
                        + remote path/archive naming (naming.go)
  docker/              docker CLI wrapper: ls/ps/inspect/Run/Exec/Stop/Start/Cp
  rclone/              rclone CLI wrapper: Upload/ListFiles/Download/Hashsum/
                        Mkdir/Prune/ListTree/ConfigureServiceAccountRemote/ListSharedDrives
  backup/<type>/       One package per backup strategy (all 8 implemented)
                        + manifest.go (SHA-256/MD5) + local_retention.go
  restore/             One file per restore strategy (all 8 implemented)
                        + verify.go (restore-time manifest re-check)
  generator/           Turns detector output into on-disk jobs + interactive wizard
  setup/               `dockvault setup`'s guided wizard
  secrets/             Platform-dispatched credential storage
  workspace/           $DOCKVAULT_HOME layout, config.json, manifest.json, jobs
  scheduler/           systemd timer / launchd job / Windows Task Scheduler instructions
  logger/, utils/      Structured logging, colors, validators, webhooks
  plugins/             Plugin interface (Name/Init/Run) + registry
plugins/docksec/       Skeleton for the planned FYP security-scanner plugin
tests/                 83 unit tests + shared docker/rclone mocks
```

See [`DOCKVAULT_CONTEXT.md`](DOCKVAULT_CONTEXT.md) for the full development
history and design decisions behind this structure.

## Architecture notes

<details>
<summary><strong>Why shell out to <code>docker</code> and <code>rclone</code> instead of using SDKs?</strong></summary>

<br>

The spec calls for a single static binary with no runtime dependencies
beyond `docker`, `rclone`, `tar`, and `gzip` — which are already required
on any host this runs on. Shelling out keeps the binary dependency-free,
and a Go SDK would add no benefit for the small subset of commands
DockVault actually needs. Backup/restore executors shell out through
`backup.DockerClient`/`backup.RcloneClient` interfaces rather than calling
`*docker.Client`/`*rclone.Client` directly, so tests can mock both without
a real daemon or `rclone` binary present.

</details>

<details>
<summary><strong>Remote path & archive naming</strong></summary>

<br>

Every backup follows a fixed convention so files land in predictable
places on Google Drive:

- **Remote directory:** `/<environment>/<hostname>/<service_type>/<job_name>/`
  — e.g. `/stage/scada-db/redis/n8n-redis-1/`. `<environment>` defaults to
  `stage` (override with `--environment` or `config.json`'s `environment`
  field); every component is lowercased.
- **Archive filename:** `<job_name>_<volume_identifier>_<UTC-timestamp>.<ext>`
  — e.g. `n8n-app_n8n_data_20260817-021300Z.tar.gz`. `<volume_identifier>`
  is a named volume's name as-is, `vol-<hash8>` for an anonymous
  (64-hex-char) volume, or `bind-<target-dir>` for a bind mount.

`internal/detector/naming.go` computes the directory and the volume
identifier at scan time; `backup.ArchiveFileName` (`internal/backup/archive.go`)
assembles the filename at the moment each executor actually writes an
archive, since the timestamp has to reflect when the backup ran.

</details>

<details>
<summary><strong>Restore semantics: replace, don't merge</strong></summary>

<br>

`postgres` restore uses `pg_dump --clean --if-exists` (emits `DROP`
statements before each `CREATE`) and `mongodb` restore uses
`mongorestore --drop`, so restoring replaces existing data rather than
appending to it — `mysqldump` already drops tables by default, no flag
needed there. This was confirmed by testing against a real container: a
plain `pg_dump`/`mongorestore` only adds rows, so a restore after data
corruption would otherwise leave corrupted rows sitting alongside the
restored ones.

</details>

<details>
<summary><strong>RabbitMQ: topology, not message bodies</strong></summary>

<br>

`rabbitmq` backs up broker topology — exchanges, queues, bindings, users,
permissions, and policies — via `rabbitmqctl export_definitions`, not a
raw copy of the data directory. This is deliberate: RabbitMQ's data
directory is a live Mnesia database that doesn't tolerate being tarred
while the broker is running, and a file-level restore would need an
exact-matching RabbitMQ/Erlang version on the target host.
`export_definitions`/`import_definitions` are versionless JSON and work
against a live, running broker — no container stop needed, and message
bodies are not included (queues are meant to be transient, not a durable
data store).

</details>

<details>
<summary><strong>What about Odoo and Traefik?</strong></summary>

<br>

Both already work today with **no dedicated executor**. Odoo's database
container is typically plain `postgres`, already covered by the
`postgres` executor; its filestore (attachments on disk) and Traefik's
ACME certs (`acme.json`) are both picked up automatically by the generic
bind-mount scan as `hostdir`/`standard` jobs. For Odoo specifically, that
means two independent jobs (DB + filestore) rather than one coordinated
backup — schedule them together so they stay reasonably close in time.

</details>

<details>
<summary><strong>Guided setup, missing credentials, and Windows Credential Manager</strong></summary>

<br>

`dockvault setup` walks through the one-time environment bootstrap:
validating a GCP service account JSON key, wiring it into an rclone
remote, listing (and letting you pick) the Shared Drive that service
account can actually see, and setting `config.json`'s defaults. It won't
create the service account itself — that needs GCP Console/OAuth access
this tool shouldn't hold — but it does copy the JSON you provide into
`$DOCKVAULT_HOME/secrets/` (mode 600) rather than leaving it wherever it
was downloaded.

Separately, `generate --interactive` prompts for any credentials a
container's own environment couldn't supply, and runs a connectivity
check against the value you enter before saving the job. `generate --auto`
stays fully non-interactive; it just lists which jobs may still be
missing credentials in its summary instead of blocking on a prompt.

**Where credentials live:** on Linux/macOS, a `.env` file per job (mode
600). On Windows, **Windows Credential Manager** — real OS-level
encryption at rest, in place of another plaintext file.

- Credential Manager is a per-user, DPAPI-backed store. A `dockvault
  backup` run by Task Scheduler needs to actually be able to unlock it,
  which depends on how the task is configured — **test the real
  scheduled task once, not just an interactive run, before trusting it
  unattended.**
- This part of the codebase was built and cross-compiled from a
  Linux-only development environment with no Windows machine to test on —
  it's `go vet`-checked, not verified end-to-end for real. See
  [`DOCKVAULT_CONTEXT.md`](DOCKVAULT_CONTEXT.md)'s "Guided Setup &
  Windows Credential Manager" section for the full detail.

</details>

<details>
<summary><strong>Durable local copy & integrity verification</strong></summary>

<br>

DockVault keeps two independent copies of every backup, satisfying the
3-2-1 rule's "2 independent media, 1 off-site" requirement:

- After every backup, the archive (and a `.manifest.json` sidecar
  recording its SHA-256, MD5, and size) is copied into
  `<local_backup_dir>/<job_name>/` and pruned to the newest N.
- Two config fields (see [Configuration](#configuration)):
  `local_backup_dir` and `local_retention_count`.
- Immediately after uploading, DockVault asks Google Drive what MD5 it
  actually received (`rclone hashsum md5`) and compares it to the local
  MD5. **A mismatch is a hard backup failure.**
- On restore, DockVault re-hashes the downloaded archive against its
  manifest *before* touching a database or container, and refuses on a
  mismatch. A backup made before this feature existed has no manifest to
  check against — that's a warning, not a refusal.

</details>

<details>
<summary><strong>Post-restore container state: never silent</strong></summary>

<br>

`redis` and `n8n` restores have to stop their container first (safely
swapping `dump.rdb` or wiping/re-extracting a volume can't happen while
the service has those files open) — but DockVault never restarts it
automatically afterward. `dockvault restore` asks explicitly, defaulting
to "leave it stopped" if you decline or the prompt can't be read. The
other five restore types never touch the container's run state at all.

</details>

## Development / testing

```bash
go test ./...                 # run all unit tests
make test                     # same, via Makefile
make test-coverage            # HTML coverage report -> coverage.html
make fmt                      # gofmt everything
make vet                      # go vet everything
```

Tests use shared `docker`/`rclone` mocks (`tests/mocks_test.go`) so the
suite runs without a real Docker daemon or `rclone` binary. See
[Implementation status](#implementation-status) below for what
additional testing against real containers has caught and fixed.

### Extending with plugins

`internal/plugins` defines a minimal `Plugin` interface
(`Name() / Init() / Run()`) and a `SecurityScanner` interface on top of
it. `plugins/docksec` is a skeleton implementation — once the FYP phase
starts, `ScanContainer` and `Remediate` get filled in there without
touching any of DockVault's core packages.

## Implementation status

Every command and backup/restore type listed above is fully implemented
and covered by the 83-test unit suite, plus validation against real
Docker containers (postgres, redis, nginx) through the actual CLI —
`generate` → `backup` → `tree`/`list` → `restore`, including deliberately
corrupting live data first to prove restore actually replaces it.

That real-container testing caught and fixed several bugs a mocked-only
suite hadn't:

- Restores that only appended instead of replacing data (fixed with
  `pg_dump --clean --if-exists` / `mongorestore --drop`).
- No signal handling, which could orphan a `docker run` child if
  `dockvault` was killed mid-backup (fixed via `main.go`'s
  `signal.NotifyContext`).
- A job with a nonzero local-retention count but no configured local
  directory silently wrote copies wherever `dockvault` happened to be run
  from, instead of nowhere (fixed by treating an empty
  `local_backup_dir` the same as retention being disabled).
- `redis-cli ping` and `mysqladmin ping` both exit 0 even with a wrong
  password, printing an auth error to stdout instead of failing the
  process — trusting the exit code alone would have silently reported
  "connected" (fixed by checking actual output content).

Building the postgres connectivity probe also surfaced a fact worth
knowing regardless of this feature: the official postgres image's default
auth config trusts every local connection unconditionally, so no
`docker exec`-based check can ever validate a postgres password is
*correct* — only that the user/database exist.

## Known limitations

- `docker exec`-based executors (postgres, mysql, mongodb, redis) require
  the target container to already be running — no "wake it up first" mode.
- Two jobs that resolve to the same name (e.g. two independent postgres
  containers both using `POSTGRES_DB=appdb`) will overwrite each other's
  `job.json` on `generate` — give them distinct names/database names.
- `backup --auto` currently behaves the same as `backup --all` (runs
  every existing job) rather than separately re-scanning and diffing.
- `manifest.json`'s (the workspace-level one) `LastSizeBytes` field is
  never populated — the size is still visible in the human-readable
  `LastBackupStatus` string. Not to be confused with the per-archive
  `<archive>.manifest.json` integrity manifest, which is fully populated.
- Windows Credential Manager (job credential storage on Windows) is
  per-user/DPAPI-backed — a `dockvault backup` run by Task Scheduler needs
  to actually be able to unlock it, which depends on the task's "log on"
  configuration. This code path was also built and cross-compiled from a
  Linux-only environment, never run on real Windows.
- The postgres connectivity check (and the `postgres` backup executor's
  own `pg_dump`) can't validate password correctness via `docker exec` —
  the official image's default auth trusts all local connections
  regardless of password.

## Troubleshooting

**`docker daemon not reachable`** — Docker isn't running, or the current
user can't reach the socket. Start Docker Desktop, or on Linux:
`sudo systemctl start docker` and ensure your user is in the `docker`
group.

**`rclone remote "dockvault_backup" is not configured`** — run
`rclone config` and create a remote named `dockvault_backup` (or pass
`--rclone-remote <name>` / set it in `config.json`).

**`schedule --install` fails writing to `/etc/systemd/system/...`** —
that needs root; re-run as `sudo dockvault schedule --install` (the
backup service itself still runs as your user, not root).

**`go: command not found` on Windows** — Go wasn't installed, or your
terminal was opened before installing it. Reopen PowerShell after running
`winget install GoLang.Go` so `PATH` picks up the new install.

**`'tar' is not recognized` on Windows** — your Windows build predates
the built-in `tar.exe`, or it's not on `PATH`. Install
[Git for Windows](https://git-scm.com/download/win) (which bundles a
`tar`/`gzip`-compatible toolchain) or use WSL.

## License

[MIT](LICENSE) © 2026 Mohamad Akram bin Mohd Faisal
