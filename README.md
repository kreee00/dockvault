# DockVault

DockVault automates backing up Docker volumes, host directories, and
service-aware database dumps (PostgreSQL, MySQL, MongoDB, Redis, n8n,
RabbitMQ) to Google Drive via `rclone`, and restoring them back. This is a Go rewrite of
an original Bash/Bashly CLI, built to support a plugin architecture for a
future security-scanning add-on (DockSec) as part of a Final Year Project.

> **Status:** feature-complete. Every command (`scan`, `generate`,
> `backup`, `restore`, `list`, `tree`, `schedule`) is fully implemented and
> has been validated against real Docker containers, not just mocked unit
> tests - see [Implementation status](#implementation-status) below for
> what that testing caught and fixed.
>
> DockVault executes backups/restores directly in Go - it does **not**
> generate `backup.sh`/`restore.sh` scripts for cron/systemd to run.
> Scheduling invokes the `dockvault` binary itself.
>
> Every backup keeps a durable local copy alongside the Google Drive
> upload, and is checksummed end to end (local SHA-256/MD5, a post-upload
> remote hash check against what Drive actually received, and a
> restore-time re-check before anything is touched) - see
> [Durable local copy & integrity verification](#durable-local-copy--integrity-verification)
> below.

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
   etc.), a job name, and a standardized
   Google Drive destination path (`/<environment>/<hostname>/<service>/<job>/`
   — see [Remote path & archive naming](#remote-path--archive-naming)) for
   each volume/path it finds.
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
   backups both locally and remotely, with webhook notifications.
6. **Restore** — pick a job and a backup file (newest first), type
   `RESTORE` to confirm. DockVault re-verifies the download against its
   manifest *before* touching a database or container, and refuses if it
   doesn't match - then, only for the two types that had to stop a
   container to do it safely (redis, n8n), asks whether to start it back
   up rather than doing that silently.

## Quick start

```bash
# Build
make build

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
additionally need `rclone` configured (`backup` checks this upfront with a
clear error and remediation hint if it isn't) - `setup` is the guided way
to get there instead of hand-running `rclone config`.

## Command reference

| Command    | Purpose                                                  |
|------------|-----------------------------------------------------------|
| `setup`    | Guided one-time setup: GCP service account, rclone remote, config defaults |
| `scan`     | Discover volumes, containers, and host paths               |
| `generate` | Create job directories (metadata + credentials) from a scan, prompting for any credentials a container's env didn't have |
| `backup`   | Execute backup jobs (single/multiple/all, parallel)         |
| `restore`  | Interactive restore from Google Drive                       |
| `list`     | ASCII tree of the local `$DOCKVAULT_HOME` workspace          |
| `tree`     | ASCII tree of the remote Google Drive folder                  |
| `schedule` | Install/check/uninstall the daily automated backup             |

Every command supports `--help` for its own flags; global flags (`--home`,
`--rclone-remote`, `--environment`, `--dry-run`, `--verbose`, `--version`)
go before the command name:

```
dockvault [global flags] <command> [command flags]
```

Ctrl+C is safe at any point - every command runs under a context cancelled
on SIGINT/SIGTERM, so an interrupted `docker run`/`docker exec` child gets
killed along with dockvault itself instead of continuing in the background.

## Architecture

```
cmd/dockvault/         CLI entrypoint; wires SIGINT/SIGTERM -> context
internal/
  cli/                 Subcommand handlers, flag parsing, dispatch
  detector/             Volume/container/host-path discovery + service inference
                         + remote path/archive naming (naming.go)
  docker/               docker CLI wrapper: ls/ps/inspect/Run/Exec/Stop/Start/Cp
  rclone/                rclone CLI wrapper: Upload/ListFiles/Download/Hashsum/Mkdir/Prune/ListTree/ConfigureServiceAccountRemote/ListSharedDrives
  backup/<type>/         One package per backup strategy, all 8 implemented
                         + manifest.go (SHA-256/MD5) + local_retention.go (durable local copy)
  restore/                One file per restore strategy, all 8 implemented
                         + verify.go (restore-time manifest re-check)
  generator/              Turns detector output into on-disk jobs (no scripts) + interactive wizard
                         (prompts for any credentials a container's env didn't have, then
                         connectivity-tests them - see "Guided setup" below)
  setup/                  dockvault setup's guided wizard: GCP service account JSON, rclone remote
  secrets/                Platform-dispatched credential storage - see "Guided setup" below
  workspace/              $DOCKVAULT_HOME layout, config.json, manifest.json, job persistence
  scheduler/              systemd timer / launchd job / Windows Task Scheduler instructions
  logger/, utils/         Structured logging, colors, validators, webhooks
  plugins/                Plugin interface (Name/Init/Run) + registry
plugins/docksec/          Skeleton for the planned FYP security-scanner plugin
tests/                    83 unit tests + shared docker/rclone mocks
```

### Extending with plugins

`internal/plugins` defines a minimal `Plugin` interface
(`Name() / Init() / Run()`) and a `SecurityScanner` interface on top of it.
`plugins/docksec` is a skeleton implementation - once the FYP phase starts,
`ScanContainer` and `Remediate` get filled in there without touching any of
DockVault's core packages.

### Why shell out to `docker` and `rclone` instead of using SDKs?

The spec calls for a single static binary with no runtime dependencies
beyond `docker`, `rclone`, `tar`, and `gzip` - which are already required
on any host this runs on. Shelling out keeps the binary dependency-free,
and a Go SDK would add no benefit for the small subset of commands
DockVault actually needs. Backup/restore executors shell out through
`backup.DockerClient`/`backup.RcloneClient` interfaces rather than calling
`*docker.Client`/`*rclone.Client` directly, so tests can mock both without
a real daemon or `rclone` binary present.

### Remote path & archive naming

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

### Restore semantics: replace, don't merge

`postgres` restore uses `pg_dump --clean --if-exists` (emits `DROP`
statements before each `CREATE`) and `mongodb` restore uses
`mongorestore --drop`, so restoring replaces existing data rather than
appending to it - `mysqldump` already drops tables by default, no flag
needed there. This was found and fixed by testing against a real
container: a plain `pg_dump`/`mongorestore` only adds rows, so a restore
after data corruption left the corrupted rows sitting alongside the
restored ones until the fix.

### RabbitMQ: topology, not message bodies

`rabbitmq` backs up broker topology - exchanges, queues, bindings, users,
permissions, and policies - via `rabbitmqctl export_definitions`, not a
raw copy of the data directory. This is deliberate: RabbitMQ's data
directory is a live Mnesia database that doesn't tolerate being tarred
while the broker is running, and a file-level restore would need an
exact-matching RabbitMQ/Erlang version on the target host.
`export_definitions`/`import_definitions` are versionless JSON and work
against a live, running broker - no container stop needed, and message
bodies are not included (queues are meant to be transient, not a durable
data store; there's no safer way to capture them without the live-copy
risk above).

### What about Odoo and Traefik?

Both already work today with **no dedicated executor**. Odoo's database
container is typically plain `postgres`, already covered by the
`postgres` executor; its filestore (attachments on disk) and Traefik's
ACME certs (`acme.json`) are both picked up automatically by the generic
bind-mount scan (`scan`/`generate` detect *any* running container's bind
mounts, not a hardcoded per-service path list) as `hostdir`/`standard`
jobs. For Odoo specifically, that means two independent jobs (DB +
filestore) rather than one coordinated backup - schedule them together so
they stay reasonably close in time.

### Guided setup, missing credentials, and Windows Credential Manager

`dockvault setup` walks through the one-time environment bootstrap that
used to be entirely manual: validating a GCP service account JSON key,
wiring it into an rclone remote, listing (and letting you pick) the
Shared Drive that service account can actually see, and setting
`config.json`'s defaults. It won't create the service account itself -
that needs GCP Console/OAuth access this tool shouldn't hold - but it
does copy the JSON you provide into `$DOCKVAULT_HOME/secrets/` (mode 600)
rather than leaving it wherever it was downloaded.

Separately, `generate --interactive` now prompts for any credentials a
container's own environment couldn't supply - a secret mounted from a
file rather than an env var, for instance - and runs a connectivity check
against the value you enter before saving the job. `generate --auto`
stays fully non-interactive; it just lists which jobs may still be
missing credentials in its summary instead of blocking on a prompt.

**Where credentials actually live:** on Linux/macOS, the existing
`.env`-per-job mechanism (mode 600), unchanged. On Windows, **Windows
Credential Manager** - a deliberate choice for real OS-level encryption
at rest on the platform this actually deploys to, in place of another
plaintext file. Two things worth knowing if that's you:

- Credential Manager is a per-user, DPAPI-backed store. A `dockvault
  backup` run by Task Scheduler needs to actually be able to unlock it,
  which depends on how the task is configured - **test the real
  scheduled task once, not just an interactive run, before trusting it
  unattended.** A read failure here surfaces as a clear, actionable error
  pointing at this rather than a confusing "credentials missing" message.
- This is the one part of this feature that was built and cross-compiled
  from a Linux-only development environment with no Windows machine to
  test on - it's `go vet`-checked, not verified end-to-end for real. See
  `DOCKVAULT_CONTEXT.md`'s "Guided Setup & Windows Credential Manager"
  section if you're picking this up and want the full detail.

### Durable local copy & integrity verification

DockVault previously kept exactly one copy of every backup - the one on
Google Drive, since local temp files are deleted immediately after upload.
Since off-site storage here is a Google Shared Drive (not a second
independent cloud provider), the 3-2-1 rule's "2 independent media, 1
off-site" is now satisfied by adding a genuine **local** durable copy:

- After every backup, the archive (and a `.manifest.json` sidecar
  recording its SHA-256, MD5, and size) is copied into
  `<local_backup_dir>/<job_name>/` and pruned to the newest N, using the
  same floor-safe algorithm that governs remote retention.
- Two config fields, settable in `config.json` (all jobs) or overridden
  per-job in `generate --interactive`:
  - `local_backup_dir` — defaults to `$DOCKVAULT_HOME/local_backups`, fully
    configurable (e.g. a separate disk or NAS mount).
  - `local_retention_count` — defaults to `2`; `0` disables the local copy
    entirely.
- Immediately after uploading, DockVault asks Google Drive what MD5 it
  actually received (`rclone hashsum md5`) and compares it to the local
  MD5. **A mismatch is a hard backup failure** — this is the one place
  "the upload API call returned success" is not trusted blindly.
- On restore, DockVault re-hashes the downloaded archive against its
  manifest *before* touching a database or container, and refuses on a
  mismatch. A backup made before this feature existed has no manifest to
  check against - that's a warning, not a refusal, since there's no way to
  retroactively verify an already-uploaded archive. For redis/n8n (the two
  restore types that stop a container), this check runs **before** the
  container is stopped, confirmed against a real running container.

### Post-restore container state: never silent

`redis` and `n8n` restores have to stop their container first (safely
swapping `dump.rdb` or wiping/re-extracting a volume can't happen while
the service has those files open) - but DockVault never restarts it
automatically afterward. `dockvault restore` asks explicitly, defaulting
to "leave it stopped" if you decline or the prompt can't be read (e.g.
non-interactive stdin). The other five restore types never touch the
container's run state at all.

## Building

```bash
make build          # bin/dockvault for your current GOOS/GOARCH
make build-all       # linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64
make docker-build    # cross-compile linux/amd64 inside a golang container
make test
make test-coverage
make install         # installs to /usr/local/bin/dockvault
```

Requires Go 1.21+. No external Go dependencies - standard library only.

## Implementation status

Everything is implemented:

- `internal/detector` — volume/container/host-path scanning, service type
  inference, credential extraction, job naming, standardized remote-path
  derivation and volume-identifier truncation
- `internal/docker` — `volume ls`/`ps`/`inspect`/`Run`/`Exec` (with
  optional stdin)/`Stop`/`Start`/`Cp`
- `internal/rclone` — `Upload`/`ListFiles`/`Download`/`Hashsum`/`Mkdir`/`Prune`/`ListTree`/
  `ConfigureServiceAccountRemote`/`ListSharedDrives`
- `internal/backup` — all 8 executors (`standard`, `postgres`, `mysql`,
  `mongodb`, `redis`, `n8n`, `hostdir`, `rabbitmq`), sharing outcome-recording/upload/
  prune bookkeeping (`run.go`), archive naming (`archive.go`), integrity
  manifests (`manifest.go`), and the durable local backup copy
  (`local_retention.go`)
- `internal/restore` — all 8 restore executors, sharing remote-file
  listing (`ListRemoteBackups`), download/decompress (`download.go`), and
  restore-time manifest re-verification (`verify.go`)
- `internal/generator` — automatic (`CreateFromVolume`/`CreateFromHostPath`)
  and interactive (`wizard.go`) job creation, including a missing-credential
  prompt + `docker exec` connectivity check (`connectivity.go`) for
  credentials a container's own environment didn't have
- `internal/setup` — `dockvault setup`'s guided wizard: GCP service
  account JSON validation, rclone remote configuration, Shared Drive
  selection, `config.json` defaults
- `internal/secrets` — platform-dispatched job-credential storage: `.env`
  files on Linux/macOS (unchanged), Windows Credential Manager on Windows
  (see [Guided setup](#guided-setup-missing-credentials-and-windows-credential-manager)
  for what that does and doesn't mean for how well-tested it is)
- `internal/workspace` — `$DOCKVAULT_HOME` layout, `config.json`
  (incl. local backup copy settings), `manifest.json`, and job persistence
  (`job.json` + platform-dispatched credential storage)
- `internal/scheduler` — real systemd timer install/check/uninstall
  (Linux), real launchd job install/check/uninstall (macOS), Windows Task
  Scheduler PowerShell instructions (printed, not auto-run - registering a
  scheduled task needs Administrator)
- All 8 `internal/cli/*.go` command handlers
- `plugins/docksec` — interface conformance only; FYP scope, intentionally
  out of scope here

Validated against real Docker containers (postgres, redis, nginx) through
the actual CLI - `generate` → `backup` → `tree`/`list` → `restore`
(including deliberately corrupting live data first to prove the restore
actually replaces it) - which caught two real bugs a mocked-only test
suite hadn't: restores that only appended instead of replacing data (fixed
with `pg_dump --clean --if-exists` / `mongorestore --drop`), and no signal
handling, which could orphan a `docker run` child if `dockvault` was
killed mid-backup (fixed via `main.go`'s `signal.NotifyContext`).

A follow-on production-readiness audit plus real-container verification
pass (see
[Durable local copy & integrity verification](#durable-local-copy--integrity-verification))
added a genuine second backup copy and end-to-end checksumming, and caught
one more real bug this way: a job with a nonzero local-retention count but
no configured local directory silently wrote copies into whatever
directory `dockvault` happened to be run from, instead of nowhere. Fixed
by treating an empty `local_backup_dir` the same as retention being
disabled.

Real-container testing for the missing-credential connectivity check (see
[Guided setup](#guided-setup-missing-credentials-and-windows-credential-manager))
caught a genuine bug too: `redis-cli ping` and `mysqladmin ping` both exit
0 even when the password is wrong, printing an auth error to stdout
instead of failing the process - trusting the exit code alone would have
silently reported "connected" for a bad password. Fixed by checking the
actual output content. Building the postgres probe also surfaced a more
fundamental fact worth knowing regardless of this feature: the official
postgres image's default auth config trusts every local connection
unconditionally, so no `docker exec`-based check - this one, or the
existing backup executor's own `pg_dump` - can ever validate a postgres
password is *correct*, only that the user/database exist.

### Known limitations

- `docker exec`-based executors (postgres, mysql, mongodb, redis) require
  the target container to already be running - no "wake it up first" mode
- Two jobs that resolve to the same name (e.g. two independent postgres
  containers both using `POSTGRES_DB=appdb`) will overwrite each other's
  `job.json` on `generate` - give them distinct names/database names
- `backup --auto` currently behaves the same as `backup --all` (run every
  existing job) rather than separately re-scanning and diffing
- `manifest.json`'s (the workspace-level one, `internal/workspace`) `LastSizeBytes`
  field is never populated (the size is still visible in the
  human-readable `LastBackupStatus` string) - not to be confused with the
  per-archive `<archive>.manifest.json` integrity manifest, which is fully
  populated
- Windows Credential Manager (job credential storage on Windows) is
  per-user/DPAPI-backed - a `dockvault backup` run by Task Scheduler needs
  to actually be able to unlock it, which depends on the task's "log on"
  configuration. This code path was also built and cross-compiled from a
  Linux-only development environment, never run on real Windows - see
  [Guided setup](#guided-setup-missing-credentials-and-windows-credential-manager)
- The postgres connectivity check (and the `postgres` backup executor's
  own `pg_dump`) can't validate password correctness via `docker exec` -
  the official image's default auth trusts all local connections
  regardless of password, confirmed for real

## Troubleshooting

**`docker daemon not reachable`** — Docker isn't running, or the current
user can't reach the socket. Start Docker Desktop, or on Linux:
`sudo systemctl start docker` and ensure your user is in the `docker`
group.

**`rclone remote "dockvault_backup" is not configured`** — run
`rclone config` and create a remote named `dockvault_backup` (or pass
`--rclone-remote <name>` / set it in `config.json`).

**`schedule --install` fails writing to `/etc/systemd/system/...`** — that
needs root; re-run as `sudo dockvault schedule --install` (the backup
service itself still runs as your user, not root - see
`scheduler.targetUser()`).
