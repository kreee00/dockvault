# DockVault Go Rewrite - Development Context

## Project Overview

DockVault is a Docker volume backup orchestration tool, rewritten from Bash/Bashly (~65KB) to Go. It discovers Docker volumes, containers, and host paths, infers the service running on each (PostgreSQL, MySQL, MongoDB, Redis, n8n, or generic), and backs them up to Google Drive via `rclone` - and restores them back.

**Target Deployment:**
- Primary dev/test: Windows (native PowerShell) + Docker Desktop
- Secondary: Ubuntu Server 22.04+ (native Linux)
- Tertiary: macOS
- **Hybrid requirement:** Must work cleanly on Windows, Linux, and macOS from the same codebase

**Status as of 2026-08-17: feature-complete, production-audited, and 3-2-1-compliant.** Every command in the spec (`scan`, `generate`, `backup`, `restore`, `list`, `tree`, `schedule`) is fully implemented, not scaffolded - see "Current Status" below. This was validated against real Docker containers (not just mocked unit tests) in this session; see "Real-Container Validation" below for what that caught. A later pass in this same session ran a full production-readiness audit (data-loss/security/DR review) and then closed its single biggest architectural finding - DockVault only ever had one durable backup copy - by adding a durable local backup copy plus end-to-end integrity verification; see "Durable Local Backup Copy & Integrity Verification" below.

## Architecture: Native Go Execution, No Generated Scripts (DECIDED 2026-08-17)

**dockvault executes backup/restore logic directly in Go - it does not generate `backup.sh`/`restore.sh`/PowerShell scripts.** An earlier same-day decision to generate OS-native scripts (PowerShell on Windows, bash on Linux) was scrapped before any script-generation code was written.

**Why:**
- Generating and maintaining two script dialects (bash + PowerShell) doubled the surface area for no real benefit
- Native Windows execution of bash scripts required Git Bash - an extra dependency to avoid entirely, not just work around
- One Go code path behaves identically on Windows PowerShell, Linux, and macOS
- Docker/rclone calls can be mocked in Go unit tests instead of mocking subprocess/script execution

**What this means concretely:**
- Every `internal/backup/<type>/*.go` and `internal/restore/*.go` executor implements real `Backup()`/`Restore()` logic (docker exec / docker run / rclone calls)
- There is no `GenerateScript()` method anywhere
- There is no `master_backup.sh` orchestrator - `dockvault schedule --install` points systemd/launchd/Task Scheduler at the `dockvault` binary itself (`dockvault backup --all --home <DOCKVAULT_HOME>`)
- A job is a directory under `$DOCKVAULT_HOME/<job-name>/` holding `job.json` (metadata) and an optional `.env` (mode 600; credentials + webhook URL) - see `internal/workspace/job.go`
- Backup/restore executors are seamed against `backup.DockerClient` / `backup.RcloneClient` interfaces (`internal/backup/executor.go`) so tests mock docker/rclone directly instead of shelling out to a real binary

## Remote Path & Archive Naming Standard (DECIDED 2026-08-17)

A DevOps-mandated convention governs every remote path and archive filename:

**Remote directory:** `/<environment>/<hostname>/<service_type>/<job_name>/` - e.g. `/stage/scada-db/redis/n8n-redis-1/`. `<environment>` defaults to `"stage"` (override via `--environment` global flag or `config.json`'s `environment` field); all four components are lowercased. Built by `detector.RemotePath()` (pure, unit-tested), used by `detectorImpl.googleDrivePath()` to populate `VolumeInfo.GoogleDrivePath`/`HostPathInfo.GoogleDrivePath`.

**Archive filename:** `<job_name>_<volume_identifier>_<ISO8601-UTC-TIMESTAMP>.<ext>` - e.g. `n8n-app_n8n_data_20260817-021300Z.tar.gz`. `<volume_identifier>` is a named volume's name as-is, `vol-<hash8>` for an anonymous (64-hex-char) volume, or `bind-<target-dir>` for a bind mount - computed by `detector.VolumeIdentifier()`/`detector.BindMountIdentifier()`, carried on `VolumeInfo`/`HostPathInfo`, then onto `backup.Job.VolumeIdentifier`. The filename itself is assembled at backup time by `backup.ArchiveFileName()`, since the timestamp must reflect when the backup actually ran.

## Durable Local Backup Copy & Integrity Verification (DECIDED 2026-08-17)

A production-readiness audit (see "Design Decisions Resolved" below for the audit itself) found DockVault satisfied only 1 of the 3 legs of the 3-2-1 backup rule: every one of the 7 backup executors writes its archive to an `os.MkdirTemp` dir and deletes it immediately after uploading to Google Drive, so **exactly one durable copy ever existed**, and nothing ever verified a backup was actually intact - "the upload call returned nil" was the sole definition of success. The user confirmed off-site storage is fixed as a Google Shared Drive (no second cloud provider available), so the fix for "2 independent media" is a genuine **local** durable copy alongside Drive, plus real integrity verification end-to-end. Both are implemented, hooked into the single choke point every executor already shared (`backup.UploadArchive`).

**Durable local copy:** after every backup, the archive (+ its manifest) is copied into `<LocalBackupDir>/<job-name>/` and pruned to the newest N using the exact same floor-safe `rclone.FilesToPrune` algorithm already used for remote retention. Controlled by two fields on `workspace.Config`/`workspace.JobConfig` (and therefore `backup.Job`): `LocalBackupDir` (`local_backup_dir` in JSON, defaults to `$DOCKVAULT_HOME/local_backups` when unset - resolved in `workspace.LoadConfig()`) and `LocalRetentionCount` (`local_retention_count`, defaults to `2`). `LocalRetentionCount <= 0` disables local retention entirely, mirroring the existing `RetentionDays <= 0` idiom. Implemented in `internal/backup/local_retention.go` (`retainLocalCopy`, `pruneLocalCopies`, `copyFile`).

**Integrity manifest:** a JSON sidecar `<archive>.manifest.json` (`internal/backup/manifest.go`) records SHA-256 + MD5 + size, computed with a single streaming read through `io.MultiWriter(sha256.New(), md5.New())` (no full-file buffering). Written locally (mode 0600) and uploaded to Google Drive alongside the archive. `rclone.ManifestSuffix = ".manifest.json"` - `ListFiles` filters these out (so they never show up in the restore picker or count toward remote retention math) but `ListTree` still shows them; `Prune` best-effort co-deletes a manifest whenever it deletes its archive.

**Post-upload remote hash verification:** immediately after uploading, DockVault asks Google Drive what MD5 it actually received (`rclone.Client.Hashsum`, which shells out to `rclone hashsum md5`) and compares it to the locally-computed MD5. **A mismatch is a hard backup failure**, not a warning - the one place in the whole pipeline where "the upload API call succeeded" stops being trusted as proof the backup is good.

**Restore-time re-verification:** every restore type re-hashes what it downloaded against the manifest (`internal/restore/verify.go`: `verifyManifestBytes`/`verifyManifestFile`) before touching a database or a live container, and refuses (hard error, nothing touched) on a mismatch. A missing/unparseable manifest (e.g. a backup taken before this feature existed) is a **warning**, not a refusal - there's no way to retroactively add integrity data to an already-uploaded archive. For redis/n8n (the two restore types that stop a container), verification happens **before** `Stop` is ever called - confirmed for real (see "Real-Container Validation" below). This required changing `RestoreExecutor.Restore`'s signature to `(warning string, err error)` across all 7 implementers plus the `internal/cli/restore.go` call site.

**Order inside `UploadArchive` (`internal/backup/run.go`):** stat archive -> compute manifest -> write manifest locally -> retain local copy -> upload archive -> upload manifest -> verify remote hash (hard fail on mismatch) -> remote prune. Local retention deliberately runs *before* the remote upload attempt and regardless of remote outcome - once remote hash verification exists, "uploaded but corrupted in transit" is a real failure mode where the local copy is the only good one, so gating local retention on remote success would throw away the one thing that makes it useful in exactly that scenario.

**Bug found during real-container verification (not by a user report):** the first version of `retainLocalCopy` only guarded on `job.LocalRetentionCount <= 0`. Testing with a hand-crafted `job.json` that had `local_retention_count: 2` but was missing the `local_backup_dir` field entirely showed `filepath.Join("", job.Name)` silently producing a relative path - dockvault created a `<job-name>/` directory relative to the current working directory instead of anywhere under `$DOCKVAULT_HOME`. Fixed by also guarding on `job.LocalBackupDir == ""`; regression test `TestRetainLocalCopy_EmptyBackupDirIsAlsoDisabled` (`tests/local_retention_test.go`) locks this in. In practice this can only happen with a hand-edited or pre-upgrade `job.json`, since `workspace.LoadConfig()` always resolves a real default path.

## RabbitMQ, Odoo, Traefik: Scoping a Real Fleet's Uncovered Services (2026-08-17)

The user shared `docker ps` output from their actual company server and asked what backup types DockVault still didn't cover. Four services didn't match any of the 7 (now 8) executors: RabbitMQ, Odoo, Prometheus, and Traefik's ACME certs. Scoping each individually (rather than assuming all four needed new executors) turned up a finding worth remembering:

**Bind-mount discovery in `internal/detector/detector.go`'s `ScanHostPaths` is already fully generic** - it inspects every running container's actual mount table and picks up any `Type == "bind"` mount unconditionally, with no hardcoded per-service path list gating it (`notableHostPaths` in `platform.go` is a separate, *additive* well-known-system-path list, not a filter bind mounts have to pass through). Combined with the fact that a service's own database container is usually already a directly-detected type (e.g. Odoo's `odoo19-db` is plain `postgres:16`), this means **several services that look "uncovered" at a glance actually need zero new code**:

- **Odoo**: the database (`postgres:16`) is already backed up correctly by the `postgres` executor. The filestore (attachments/documents on disk, not in the DB) is picked up automatically as a `hostdir`/`standard` job via the generic bind-mount or named-volume scan - no Odoo-specific detection was added. Two independent jobs today (`postgres` for the DB, `hostdir`/`standard` for the filestore), not one coordinated job - `backup.Job`/`workspace.JobConfig` have no field for "a second data source," and adding one for this alone wasn't judged worth the schema change (see below). Operational recommendation: schedule both jobs together so they stay reasonably close in time.
- **Traefik**: `acme.json` (or its containing directory) is picked up automatically as a bind-mount `hostdir` job the same way, with no Traefik-specific code. Caveat inherited from the existing architecture, not new to Traefik: bind-mount discovery only sees *running* containers' mount tables (`ScanHostPaths`/`ScanVolumes` both call `ListContainers(ctx, false)`), so a stopped Traefik container's mount is missed at scan time - a general limitation (see "Known Limitations" #1's `docker exec`-requires-running note), not something worth new code to special-case for one service.
- **Prometheus**: explicitly descoped by the user - it's monitoring-only, not data worth protecting. (For the record, if this changes later: the official `prom/prometheus` image ships no shell/curl at all, so a proper snapshot-API-based backup would need DockVault itself to speak HTTP to the container's published port - a real new capability, not just a new executor - plus the `--web.enable-admin-api` prerequisite. The generic `standard` tar fallback already works for it today at low real risk, just without that consistency guarantee.)

**RabbitMQ was the one gap that needed real code** - see the `rabbitmq` executor pair described in "Current Status" above. Design trade-off worth restating here since it's easy to assume otherwise: **backup captures topology only (`rabbitmqctl export_definitions`: exchanges, queues, bindings, users, permissions, policies), not message bodies.** This is deliberate, not a shortcut - RabbitMQ's own data directory is a live Mnesia database that doesn't tolerate being tarred while running (risk of a torn/corrupt copy), and a file-level restore would need an exact-matching RabbitMQ/Erlang version on the target host. `export_definitions`/`import_definitions` are versionless and work against a live, running broker (no container stop needed, unlike redis/n8n). Verified for real: declared a queue/exchange/binding on a live `rabbitmq:3-management` test container, backed it up, deleted the queue (simulating loss), restored from the real Google Shared Drive, confirmed the queue/binding reappeared - then separately tampered the uploaded archive on Drive and confirmed restore refused with a hash mismatch, broker state and container uptime both unchanged throughout.

## Guided Setup & Windows Credential Manager (2026-08-17, later same day again)

The company server this actually deploys to is Windows, and two operational gaps surfaced when asked how credentials get provisioned there in practice: the GCP service account JSON / rclone remote setup was entirely manual (done by hand via `rclone config create` + `rclone backend drives` during the RabbitMQ verification session above), and a container whose credentials weren't fully recoverable from its own env vars (a secret mounted from a file, a `*_PASSWORD` never actually set) silently got a job with partial credentials that only failed later, at backup time.

**`dockvault setup`** (new command, `internal/setup`) is a guided one-time environment bootstrap: validates a GCP service account JSON someone already downloaded (`ValidateServiceAccountJSON` - structural checks only, `type == "service_account"` + required fields; can't and doesn't contact GCP), copies it into `$DOCKVAULT_HOME/secrets/gcp-service-account.json` (0600, best-effort `icacls` ACL tightening on Windows via `internal/setup`'s `tightenACLsBestEffort` - `os.Chmod` on Windows only toggles the read-only attribute, not real ACLs), configures an rclone remote against it (`rclone.Client.ConfigureServiceAccountRemote`), lists the Shared Drives that service account can actually see (`ListSharedDrives`, via `rclone backend drives` - genuinely exercises the credentials against the real Drive API, unlike `config create` which accepts a bogus path/ID without complaint at write time, confirmed for real), lets the user pick one, and walks through `config.json`'s defaults. Verified for real end to end against the actual `dockvault_gdrive_stage` Shared Drive: ran the full wizard, confirmed the resulting remote/config/secrets file were all correct, then ran a real `dockvault backup` against the remote it had just configured and confirmed the archive landed correctly.

**`generate --interactive`'s wizard now prompts for credentials the container's own environment couldn't supply** (`generator.Wizard.PromptMissingCredentials`, exported for direct testability): after building a job, any of its service type's curated credential keys (`detector.CredentialEnvKeysFor`, a new exported wrapper over the existing `credentialEnvKeys` map) still empty get prompted for, then connectivity-tested via `docker exec` (`internal/generator/connectivity.go`) before the job overrides step. Best-effort, not blocking - a failed probe warns and still lets the job be saved. `generate --auto` stays non-interactive by design; it prints a post-run summary of which auto-created jobs are still missing credentials instead of prompting.

**Real-container testing caught a genuine bug in the first version of these connectivity probes, not a hypothetical one:** `redis-cli ping` and `mysqladmin ping` both **exit 0 even when authentication fails**, printing `NOAUTH .../Access denied ...` to stdout instead of failing the process - confirmed directly (`docker exec test-redis-secretfile redis-cli -a wrongpass123 --no-auth-warning ping` → prints the AUTH error, exits 0; same for `mysqladmin ping -u root -pwrongpass` against a real mysql:8 container). Trusting `err == nil` alone, the first version's approach, silently reported "connected" for a wrong password on both. Fixed by checking the actual output content (`bytes.Contains(out, []byte("PONG"))` / `"mysqld is alive"`), not just the exit code - locked in by `TestPromptMissingCredentials_AuthFailureWithZeroExitIsStillDetected` using the exact captured failure output as the mock's response.

**A second, more fundamental discovery while building the postgres probe: the official `postgres` image's default `pg_hba.conf` trusts every *local* connection unconditionally, regardless of password** - `local all all trust` and `host all all 127.0.0.1/32 trust` (only genuinely remote addresses hit `scram-sha-256`). Confirmed for real: a deliberately wrong `PGPASSWORD` still connects and runs a query, both over the Unix socket and over `-h 127.0.0.1`. This means **no `docker exec`-based check - this probe, or the existing `postgres` backup executor's own `pg_dump` - can ever validate that a postgres password is *correct*, only that the user/database *exist*** (checked by switching the probe from `pg_isready`, which doesn't even do that much - it reports "accepting connections" for a nonexistent db/user too, confirmed for real - to `psql -U ... -d ... -c "SELECT 1"`, which does fail correctly on a nonexistent role or database). Not a bug to fix - a real, disclosed limitation of the local-exec approach, documented in `connectivity.go`'s doc comment. Also worth noting for anyone debugging a "why did this backup succeed with what I thought was the wrong password" question later: the existing `postgres` backup executor has the exact same property, for the exact same reason - it isn't new to this session's code.

**On the credential-storage architecture decision itself:** offered a lower-risk alternative (keep the existing file-based `.env` mechanism everywhere, including Windows), the user explicitly chose real OS-level encryption via **Windows Credential Manager** instead. This is the project's first platform-specific (`//go:build`) code, and the following is a real, load-bearing caveat rather than a formality: **the development environment for this feature was Linux-only, with no Windows machine available** - `internal/secrets/store_windows.go` and `internal/workspace/env_windows.go` were cross-compiled (`GOOS=windows GOARCH=amd64`) and `go vet`-checked successfully, but `CredWriteW`/`CredReadW` have never actually been called for real. Treat this as reviewed, not battle-tested, until exercised on the actual deployment target - see "Known Limitations" below for the specific Task Scheduler risk this carries. Implemented via the standard library's own `syscall.NewLazyDLL`/`NewProc` (not `golang.org/x/sys/windows`), keeping this project's zero-external-Go-dependency principle intact - confirmed `go.mod` still has zero `require` entries.

## Current Status (as of 2026-08-17): Feature-Complete

### Fully Implemented & Tested

- **`internal/detector`** - volume/container/host-path scanning, service type inference (image name + port hints), credential extraction, job naming, remote-path/volume-identifier naming (`naming.go`)
- **`internal/docker`** - CLI wrapper: `volume ls`, `ps`, `inspect`, `Run()` (throwaway containers), `Exec()` (with optional stdin, for both dump-taking and dump-restoring), `Stop()`/`Start()`/`Cp()`
- **`internal/rclone`** - `Upload`, `ListFiles` (newest-first, skips `.manifest.json` sidecars), `Download`, `Hashsum` (MD5 via `rclone hashsum md5`, for post-upload verification), `Prune` (only deletes once file count > min_files_to_keep, co-deletes each archive's manifest), `ListTree` (recursive, for `dockvault tree`, shows manifests), `Mkdir` (sequential pre-creation for parallel backups, see decision #13), `ConfigureServiceAccountRemote`/`ListSharedDrives` (`rclone config create .../rclone backend drives`, for `dockvault setup` - see "Guided Setup" section below)
- **`internal/backup`** - all 8 executors real: `standard` (tar via throwaway alpine), `postgres` (`pg_dump --clean --if-exists` | local gzip), `mysql` (`mysqldump`, password via `MYSQL_PWD` env not argv), `mongodb` (`mongodump --archive --gzip`), `redis` (BGSAVE + poll LASTSAVE + `docker cp`), `n8n` (stop/tar/always-restart via a deferred, named-return-based guarantee even on failure), `hostdir` (tar a host path, no volume), `rabbitmq` (`rabbitmqctl export_definitions` | local gzip - topology, not message bodies, see "RabbitMQ, Odoo, Traefik" section below). Shared helpers in `internal/backup/run.go` (`RunAndRecord`, `UploadArchive`, `PruneWithWarning`), `archive.go` (`ArchiveFileName`, `GzipToFile` - shared by postgres/mysql/rabbitmq), `manifest.go` (`ComputeManifest`/`WriteManifest`, SHA-256+MD5), and `local_retention.go` (`retainLocalCopy`, the durable local copy - see the dedicated section above) keep the 8 executors from duplicating the same bookkeeping.
- **`internal/restore`** - all 8 restore executors real, sharing `ListRemoteBackups()` and manifest re-verification (`verify.go`). `standard`/`hostdir` extract-over (no wipe, per original spec wording); `postgres`/`mysql`/`mongodb` (`--drop`) feed the (decompressed, where needed) dump into the live container over stdin; `redis`/`n8n` stop the container (required to safely swap files) and **deliberately leave it stopped** - see the resolved design decision below; `rabbitmq` runs `rabbitmqctl import_definitions` live, no stop needed. Every executor now returns `(warning string, err error)` from `Restore()` - see the "Durable Local Backup Copy & Integrity Verification" section above.
- **`internal/workspace`** - `$DOCKVAULT_HOME` layout, `config.json` (incl. `local_backup_dir`/`local_retention_count`), `manifest.json`, and job persistence (`job.go`: `SaveJob`/`LoadJob`/`ListJobNames`/`LoadAllJobs`, `job.json` + platform-dispatched credential storage - `.env` on Linux/macOS (`env_unix.go`), Windows Credential Manager on Windows (`env_windows.go`, via `internal/secrets`) - see "Guided Setup & Windows Credential Manager" section below
- **`internal/secrets`** - `Store` interface + a real Windows Credential Manager implementation (`store_windows.go`, `//go:build windows`) - this project's first platform-specific code, see below for what that does and doesn't mean for how well-tested it is
- **`internal/setup`** - `dockvault setup`'s guided wizard: GCP service account JSON validation (`gcp.go`), rclone remote configuration + real Shared-Drive-access confirmation, `config.json` defaults
- **`internal/generator`** - `CreateFromVolume`/`CreateFromHostPath` (build a `Job` from detector output + config defaults, write it via `workspace.SaveJob`), `ExecutorNameForServiceType`, and a working interactive wizard (`wizard.go`: scan → pick → fill in any credentials that couldn't be auto-detected (`PromptMissingCredentials`, with a per-service-type `docker exec` connectivity probe - `connectivity.go`) → per-job retention/local-retention/webhook overrides → confirm → write, looping until "Finish")
- **`internal/scheduler`** - real systemd timer install/check/uninstall (Linux), real launchd job install/check/uninstall (macOS), Windows Task Scheduler PowerShell instructions (printed, not auto-run, since it needs Administrator)
- **All 8 CLI commands** (`scan`, `generate`, `backup`, `restore`, `list`, `tree`, `schedule`, `setup`) are implemented, not stubs - see each `internal/cli/*.go` file
- **Context/signal handling**: `main.go` sets up `signal.NotifyContext` on SIGINT/SIGTERM and threads it through every command, so Ctrl+C (or a killed parent process) cancels in-flight `docker run`/`docker exec` children instead of orphaning them - see "Real-Container Validation" below for why this was added
- **Unit tests**: 83 tests across detector naming (incl. `rabbitmq` image detection, `CredentialEnvKeysFor`), executor registries, all 8 backup executors (mocked docker/rclone), manifest computation, local retention pruning, remote hash verification (success + hard-fail on mismatch), unique-remote-dir dedup for parallel backup pre-creation, all 8 restore executors including manifest-mismatch-refuses-before-touching-anything cases, workspace/generator config round-tripping, generator job-building, GCP service account JSON validation, rclone Shared Drive JSON parsing, and the generator wizard's missing-credential prompt + connectivity probe (incl. a locked-in regression for the exit-0-on-auth-failure bug described below) - `go build`/`go vet`/`go test`/`go test -race`/`gofmt -l .`/`govulncheck` all clean, cross-compiles clean for linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64

### Real-Container Validation (2026-08-17)

Ran the actual CLI (not a harness) against real postgres (named volume, seeded with data), redis (anonymous volume), and nginx (bind mount) containers, plus a local-filesystem rclone `alias` remote standing in for Google Drive:

- `generate --auto` → `backup --job <name>` for postgres/redis/hostdir → verified real uploaded archive names against the naming convention → `tree --detailed` renders the real remote hierarchy → `restore --job <name>` (typed `RESTORE` confirmation, real `psql`/`docker cp` restore) → verified restored data matches, including deliberately corrupting live data first to prove the restore actually replaces it.

This caught real bugs a mocked-only test suite didn't:

1. **`pg_dump`/`mongorestore` didn't truly replace data.** A plain `pg_dump` only emits `INSERT`s (no `DROP`/`TRUNCATE`), so restoring it appended to whatever was already there instead of replacing it - confirmed by corrupting a table, restoring, and seeing the corrupted row survive alongside the restored ones. Fixed: `pg_dump --clean --if-exists` (postgres) and `mongorestore --drop` (mongodb) now make restores true replacements. (`mysqldump` already includes `DROP TABLE IF EXISTS` by default - no fix needed there.)
2. **No signal handling → orphaned containers.** `runBackup` (and other commands) used `context.Background()` with nothing to cancel it. Backing up `/home` (a large real directory) ran long; killing the `dockvault` process left its `docker run ... tar` child and the throwaway alpine container still running in the background. Fixed: `main.go` now wires `signal.NotifyContext` through every command via a new `ctx context.Context` parameter on `command.run`, so Ctrl+C actually stops in-flight docker children - verified by re-running and confirming no orphaned process/container after a kill.
3. (Carried over from the previous session's real-container testing, still relevant context: anonymous-volume hashes leaking into job names, and `--flag value` global-flag parsing - both already fixed then.)

### Real-Container Validation: Durable Local Backup Copy & Integrity Verification (2026-08-17, later same day)

Re-ran the same real-infrastructure methodology after implementing local retention + manifests + remote hash verification:

1. Ran a real backup against the live `test-postgres` container; confirmed both the archive and its `.manifest.json` landed in the local rclone `alias` remote (standing in for Google Drive) *and* under `$DOCKVAULT_HOME/local_backups/<job>/`.
2. Confirmed a genuinely corrupted/tampered remote archive is detected and restoration is refused, with the live database's data verified unchanged afterward (same corrupt-then-restore-then-verify methodology as the postgres/mongodb replace-semantics testing above, this time proving the *refusal* path rather than the restore path).
3. For redis (a restore type that must stop its container to restore safely): forced a manifest mismatch and confirmed via `docker ps` that the container's uptime was unchanged (never stopped) and via `redis-cli GET` that the original key's value was still present - i.e. verification really does run before `Stop`, not just in the code's read order.
4. Found and fixed the empty-`LocalBackupDir` bug described in the section above - this was caught by this verification step itself, not reported by a user, which is exactly what real-container testing is for in this project.

## Key Design Decisions

### Why Shell Out to docker/rclone Instead of SDKs?
- Spec requirement: single static binary, no deps beyond docker/rclone/tar/gzip
- Go SDK adds no benefit for the subset of commands dockvault needs

### Why Two/Three-Way Platform Detection?
- **PlatformLinux** - systemd timer
- **PlatformDarwin** - launchd user agent (`~/Library/LaunchAgents`, no root needed)
- **PlatformWindows** - native Windows binary (GOOS=windows, PowerShell) - Task Scheduler instructions are printed, not auto-run, since registering a scheduled task needs Administrator and dockvault shouldn't silently elevate
- All three ultimately just invoke `dockvault backup --all` directly - no wrapper script

### Windows Host Path Scanning
- Looks for Windows-idiomatic paths (`C:\nginx\conf`, `C:\ProgramData\letsencrypt`)
- Scans `%SystemDrive%\ProgramData` and `%SystemDrive%\Users` (respects `%USERPROFILE%` for multi-user/scheduled-task contexts)
- Avoids Unix-isms (`/etc/nginx`, `/home`, `/opt`) which don't exist on Windows
- Skip-list prevents crawling into AppData, node_modules, .git, etc.

### Job Persistence Format
A job directory (`$DOCKVAULT_HOME/<job-name>/`) holds:
- `job.json` - non-secret metadata (`workspace.JobConfig`): name, `backup_type` (which executor - see below), service type, volume/host-path identity, container name, remote path, retention settings
- `.env` (mode 600, only written if non-empty) - `KEY=VALUE` lines for credentials plus `DOCKVAULT_WEBHOOK_URL`

`backup_type` is resolved once at generate time by `generator.ExecutorNameForServiceType` - equal to the detector's service type for every service except `generic`, which maps to the `standard` (plain tar) executor. Storing it on the job avoids re-deriving it every time `backup`/`restore` load a job.

## Important Notes for Claude Code

### Testing
- No docker daemon is guaranteed available in every environment this runs in - use the seamed `backup.DockerClient`/`backup.RcloneClient` interfaces and the shared mock in `tests/mocks_test.go` (`mockDockerClient`/`mockRcloneClient`) rather than mocking subprocess execution
- Unit tests live in `tests/` (external package, not `internal/`)
- Use test seams (e.g., `detector.SetGOOSForTest()`, `redis.SetPollIntervalForTest()`) for OS-aware/timing-sensitive logic rather than sleeping for real in tests
- **Periodically test against real Docker containers, not just mocks** - this session's two real bugs (`pg_dump --clean`, signal handling) were both invisible to the mocked unit test suite and only surfaced by actually running `generate`/`backup`/`restore` against live containers

### Cross-Platform Compatibility
- Always use `filepath.Join()` for local paths; Google Drive remote paths are always forward-slash (use the `path` package or literal `/`, never `filepath`, since they're not OS paths) - see `detector.BindMountIdentifier` for the pattern
- `runtime.GOOS` for platform detection: `"linux"`, `"darwin"`, or `"windows"`
- Windows paths: `\` in Go strings need escaping or raw strings (backticks)

### Error Messages
- Always include remediation hints: "Docker not found - try `apt install docker.io` or `brew install docker`"
- Never swallow errors silently; surface them to the CLI with context
- Log-and-continue for non-critical failures (one container exiting mid-scan shouldn't fail the whole scan; a failed webhook or prune shouldn't fail an otherwise-successful backup; one bad job directory shouldn't fail `LoadAllJobs` for every other job)

### Code Style
- No external dependencies except standard library (Go 1.21+)
- Comments on exported types/functions/consts required; doc comment for each package
- Shared logic used by 3+ near-identical implementations (all 7 backup executors, all 7 restore executors) gets extracted once it's proven out on the first one or two - see `internal/backup/run.go`, `restore.ListRemoteBackups`

### Workflow
1. When implementing a component, start with tests if possible
2. Build locally: `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`
3. Cross-compile before committing: `make build-all`
4. Periodically validate against real Docker containers (see "Real-Container Validation" above) - don't rely solely on mocked tests for a tool whose entire job is shelling out to `docker`/`rclone`

## File Structure Quick Reference

```
cmd/dockvault/main.go                    # CLI entrypoint; wires SIGINT/SIGTERM -> ctx
internal/
  version/                               # Build-time version vars
  cli/                                   # Subcommand handlers, flag parsing, dispatch
    root.go                              # Dispatcher; command.run takes (ctx, Global, args)
    registry.go                          # buildBackupRegistry/buildRestoreRegistry, resolveRcloneRemote
    {scan,generate,backup,restore,list,tree,schedule,setup}.go
  detector/                              # Volume/container/path discovery + service inference
    detector.go                          # Main detector logic
    service_types.go                     # Service detection tables
    platform.go                          # OS-aware host paths
    naming.go                            # RemotePath, VolumeIdentifier, BindMountIdentifier, NormalizeEnvironment
  docker/                                # docker CLI wrapper: ls/ps/inspect/Run/Exec/Stop/Start/Cp
  backup/                                # Backup executor interface + all 8 implementations
    executor.go                          # BackupExecutor, DockerClient/RcloneClient seams, Job (incl. LocalBackupDir/LocalRetentionCount)
    run.go                               # RunAndRecord, UploadArchive (+ verifyRemoteHash), PruneWithWarning, VolumeIdentifierOrFallback, UniqueRemoteDirs
    archive.go                           # ArchiveFileName, GzipToFile (shared by postgres/mysql/rabbitmq)
    manifest.go                          # ArchiveManifest, ComputeManifest, WriteManifest, ManifestPathFor
    local_retention.go                   # retainLocalCopy, pruneLocalCopies, copyFile - the durable local backup copy
    size.go                              # FormatBytes
    {standard,postgres,mysql,mongodb,redis,n8n,hostdir,rabbitmq}/
  restore/                               # Restore executor interface + all 8 implementations
    restorer.go                          # RestoreExecutor (Restore returns (warning, err)), ListRemoteBackups, StopsContainerDuringRestore
    download.go                          # downloadAndRead (download + optional gunzip + manifest verify)
    verify.go                            # verifyManifestBytes/verifyManifestFile - restore-time integrity re-check
    {standard,postgres,mysql,mongodb,redis,n8n,hostdir}_restore.go, rabbitmq_restore.go
  generator/                             # Job creation from detector output
    job_generator.go                     # CreateFromVolume, CreateFromHostPath, ExecutorNameForServiceType
    wizard.go                            # generate --interactive, incl. PromptMissingCredentials
    connectivity.go                      # per-service-type docker exec connectivity probe (postgres/mysql/mongodb/redis)
  setup/                                 # dockvault setup's guided wizard
    wizard.go                            # SA JSON -> rclone remote -> Shared Drive pick -> config.json defaults
    gcp.go                               # ValidateServiceAccountJSON
  secrets/                               # Platform-dispatched credential storage backend
    store.go                             # Store interface, no build tag
    store_windows.go                     # Windows Credential Manager (//go:build windows) - see decision #15
  rclone/                                # rclone CLI wrapper: Upload/ListFiles/Download/Hashsum/Mkdir/Prune/ListTree
    client.go                            # ManifestSuffix const, Hashsum (MD5 via `rclone hashsum md5`), Mkdir (sequential pre-creation, see decision #13 below), ConfigureServiceAccountRemote/ListSharedDrives/ParseBackendDrives
  workspace/                             # $DOCKVAULT_HOME management
    workspace.go, config.go, manifest.go, job.go   # config.go/job.go carry local_backup_dir/local_retention_count
    env_unix.go, env_windows.go          # saveEnv/loadEnv split by platform (see decision #15)
  scheduler/                             # Scheduling
    systemd.go                           # Linux systemd timer + Platform detection + shared runCmd
    launchd.go                           # macOS launchd user agent
    windows.go                           # Windows Task Scheduler PowerShell instructions
  logger/, utils/, plugins/              # Supporting packages
plugins/docksec/                         # Security scanner plugin skeleton (FYP scope, out of scope here)
tests/                                   # 83 unit tests
  mocks_test.go                          # Shared mockDockerClient/mockRcloneClient (records every Upload/Download/Hashsum call)
  detector_test.go, naming_test.go       # Detector + naming convention tests, incl. CredentialEnvKeysFor
  backup_test.go, service_backup_test.go, standard_backup_test.go, rabbitmq_backup_test.go  # All 8 backup executors
  manifest_test.go, local_retention_test.go  # Manifest hashing + durable local copy pruning
  restore_test.go                        # Restore executors, incl. manifest-mismatch-refuses-before-Stop/touching-broker cases
  workspace_test.go, generator_test.go   # Job persistence + generation, incl. local-retention field round-trips
  wizard_credentials_test.go             # PromptMissingCredentials + connectivity probe, incl. the exit-0-on-auth-failure regression
  gcp_setup_test.go, rclone_client_test.go  # ValidateServiceAccountJSON, ParseBackendDrives (real captured sample)
  platform_test.go                       # OS-aware path tables
  fixtures/                              # Mock docker/rclone output (legacy fixtures, superseded by mocks_test.go for new tests)
```

## Known Limitations / TBD

1. **`docker exec` requires the target container to be running.** Service-specific backup executors (postgres, mysql, mongodb, redis) don't detect or handle a stopped container - the `docker exec` simply fails with a clear error. No "wake up, back up, sleep" mode.
2. **Job name collisions aren't detected.** Two volumes that resolve to the same job name (e.g. two independent postgres containers both named `POSTGRES_DB=appdb`) silently overwrite each other's `job.json` on `generate` - observed during real-container testing (two test postgres containers with matching credentials). Not a crash, but data model doesn't disambiguate; a real deployment should give distinct `POSTGRES_DB`/volume names.
3. **`manifest.json`'s `LastSizeBytes` is never populated.** `Job.LastBackupStatus` carries a human-readable size string ("success (2.3 MB)"), but nothing parses it back into the manifest's `LastSizeBytes int64` field. Low priority - `LastBackupStatus` already shows the size to a human reader.
4. **`backup --auto` is currently equivalent to `--all`.** The spec's distinction ("detect all volumes and backup only those with generated jobs") isn't separately implemented - both run every job already on disk. Would need `--auto` to re-scan and diff against existing jobs to differ meaningfully from `--all`.
5. **`resolveRcloneRemote`'s "was --rclone-remote explicitly passed" detection is a string comparison against the hardcoded default**, not real flag-provenance tracking (`flag.FlagSet` doesn't cheaply expose that). Only matters if a user's `config.json` wants to override the remote *and* they never pass `--rclone-remote` - works correctly, just via a slightly fragile heuristic.
6. **Windows Credential Manager is per-user/DPAPI-backed - a scheduled `dockvault backup` may not be able to unlock it, depending on Task Scheduler configuration.** Whether "Run only when user is logged on" vs. "Run whether user is logged on or not" actually preserves access to the executing user's credential vault is a real Windows session/token semantics question this project has not verified on the real deployment target (see "Guided Setup & Windows Credential Manager" above - the whole feature was built and cross-compiled from a Linux-only environment). `env_windows.go`'s `loadEnv` surfaces a real read failure as a loud, actionable error explaining this rather than a silent empty-credentials result, but that doesn't make the underlying scheduled-task configuration problem go away - **anyone deploying this on Windows should run `dockvault backup` once via the actual configured scheduled task (not just interactively) before trusting it unattended.**
7. **The postgres connectivity probe (and the `postgres` backup executor's own `pg_dump`) can't validate password correctness via `docker exec`.** The official postgres image's default `pg_hba.conf` trusts all local connections (Unix socket and `127.0.0.1`/`::1` over TCP) regardless of password - confirmed for real, not assumed. A wrong `POSTGRES_PASSWORD` still works for both the connectivity check and an actual backup, as long as the user/database names are right. See "Guided Setup & Windows Credential Manager" above for the full writeup.

## Design Decisions Resolved 2026-08-17

1. **Script generation:** scrapped entirely in favor of native Go execution - see Architecture section above.
2. **Restore on stopped containers / post-restore container state:** `restore` does **not** silently restart containers. After a restore completes, `dockvault restore` prompts the user (interactively) whether to start/restart the container - default is to leave it as-is if they decline or the prompt can't be answered (non-interactive stdin). Implemented via `restore.StopsContainerDuringRestore()` (only redis/n8n stop a container at all) + `cli.maybeRestartContainer()`. Verified for real: restored redis data, declined the restart prompt, confirmed the container stayed `Exited`, then manually started it and confirmed the restored key was present.
3. **Credential validation:** `generate` trusts credentials as given and does not pre-validate them; a bad credential surfaces as a failure on the first actual backup attempt.
4. **Remote path & archive naming standard:** see the dedicated section above.
5. **Job persistence format:** `job.json` (metadata) + `.env` (secrets, mode 600) per job directory - see "Job Persistence Format" above.
6. **`pg_dump --clean --if-exists` / `mongorestore --drop`:** added after real-container testing showed restores were append-only rather than replacing existing data - see "Real-Container Validation" above.
7. **Context/signal handling:** every CLI command now receives a `ctx context.Context` cancelled on SIGINT/SIGTERM (wired in `main.go`), so Ctrl+C stops in-flight `docker` children instead of orphaning them - see "Real-Container Validation" above.
8. **Production-readiness audit:** a full SRE/security/DR-style audit was run against the codebase (backup integrity, silent failures, credential leakage, retention safety, restore-testing, crash-safety, etc.). Fixes already folded into the sections above include `docker.RedactArgs` (credential leakage into error strings/logs/webhooks), `rclone.FilesToPrune` (retention could previously delete below `min_files_to_keep`), and the items in #6/#7. The audit's single largest remaining finding - only one durable backup copy ever existed, with no integrity verification anywhere - is resolved by items #9-12 below.
9. **Durable local backup copy:** given the fixed constraint that off-site storage is a Google Shared Drive only (no second cloud provider available), the 3-2-1 gap is closed with a genuine local copy instead - see "Durable Local Backup Copy & Integrity Verification" above.
10. **Local retention defaults:** local copies live under `$DOCKVAULT_HOME/local_backups/<job>/` by default but the location is fully configurable (`local_backup_dir`); 2 copies are kept per job by default (`local_retention_count`), both overridable globally (`config.json`) or per-job (`generate --interactive`).
11. **Post-upload verification is a real remote hash check, not just a locally-recorded checksum used later** - `rclone hashsum md5` against what Google Drive actually received, compared to the local MD5, hard-failing the backup on mismatch. This was an explicit user decision (over the cheaper alternative of local-only verification deferred to restore time).
12. **`RestoreExecutor.Restore` returns `(warning string, err error)`:** the one restore-side interface change this required, so "restored without anything to verify against" (pre-existing backups made before this feature shipped) surfaces to the operator instead of being silently swallowed - verified for real that redis/n8n check the manifest *before* stopping their container, not after.
13. **Parallel backups pre-create every destination directory sequentially before starting.** Found via real testing against an actual Google Shared Drive (not the local `alias` remote used elsewhere - Drive's lack of atomic "create directory if missing" doesn't reproduce over a POSIX filesystem): the default `backup --all` (`--parallel 3`) reliably created duplicate same-named remote folders when multiple jobs' first-ever backups shared an ancestor directory, since concurrent `rclone copy` subprocesses each independently found it missing and each created their own - nondeterministically breaking the new post-upload `Hashsum` verification ("directory not found") depending on which duplicate a job's upload landed in. Fixed: `backup.UniqueRemoteDirs(jobs)` (`internal/backup/run.go`) computes the distinct set of destination directories about to be used; `rclone.Client.Mkdir` (`internal/rclone/client.go`) is called once per unique directory, sequentially, in `cli.runBackup` before the parallel job pool starts - so by the time concurrent uploads run, nothing is left to race on. Confirmed via controlled A/B against the real Shared Drive: `--parallel 1` was always clean, `--parallel 3` reproduced duplicates every time before the fix and was clean after it.
14. **RabbitMQ added as an 8th service type; Odoo/Traefik/Prometheus deliberately did not get new executors.** See "RabbitMQ, Odoo, Traefik: Scoping a Real Fleet's Uncovered Services" above for the full reasoning - bind-mount discovery already generically covers Odoo's filestore and Traefik's ACME certs, Prometheus was descoped by the user as monitoring-only, and RabbitMQ backs up topology (`rabbitmqctl export_definitions`) rather than message bodies, which is the industry-standard, corruption-safe approach for a broker that must stay running during backup. `backup.GzipToFile` was extracted from postgres/mysql's previously-duplicated local `gzipToFile` helper into `internal/backup/archive.go` once rabbitmq would have made it a third copy, per this project's own stated "3+ near-identical implementations get extracted" convention.
15. **Windows Credential Manager chosen over keeping the existing file-based `.env` mechanism everywhere.** Offered the lower-risk alternative explicitly, the user chose real OS-level encryption at rest on the actual Windows deployment target instead. Implemented as the sole store for job secrets on Windows (not a dual-write with `.env` - writing the real secret to both would defeat the point), via the standard library's own `syscall.NewLazyDLL`/`NewProc` rather than `golang.org/x/sys/windows`, keeping this project's zero-dependency principle intact. See "Guided Setup & Windows Credential Manager" above for the full writeup, including why this is reviewed-but-not-battle-tested (built and cross-compiled from a Linux-only environment) and the Task Scheduler risk documented in "Known Limitations" #6.
16. **Connectivity probes check output content, not just exit code, for mysql/redis - but even that can't validate a postgres password.** A real bug (`redis-cli`/`mysqladmin ping` exiting 0 on auth failure) was caught by testing against live containers, not assumed from docs, and fixed before it shipped. See "Guided Setup & Windows Credential Manager" above and "Known Limitations" #7 for the postgres-specific limitation that isn't fixable this way at all.

## Real Google Shared Drive Validation (2026-08-17)

Beyond the local `alias`-remote testing described elsewhere in this doc, the full pipeline (upload, post-upload hash verification, local + remote retention pruning, restore, and tamper-refusal) was validated against an actual Google Shared Drive via a real GCP service account, using `rclone` remote `dockvault_gdrive_stage` (type=drive, scope=drive, `team_drive=<Shared Drive ID>`) - kept separate from the local-alias `dockvault_backup` remote so both stay usable. This is what caught the parallel-directory-creation race (#13 above). Also reconfirmed for real, this time against Drive's actual API rather than a mock:

- **Remote retention's count-floor + age-intersection safety property**: backdating one of several same-age archives via `rclone touch -t`, then triggering a prune, deleted only the archive that was both beyond the newest-N floor *and* past the retention window - a different archive that was beyond the floor but not old enough correctly survived. Manifest sidecar co-deletion on prune confirmed too.
- **Tamper-refusal, including the critical "never stop the container on a bad backup" property**: overwrote a just-uploaded archive on Drive directly with garbage bytes (leaving its manifest, which still recorded the original good hash, untouched) - `restore` refused with a SHA-256 mismatch for both postgres (live data unchanged) and redis (confirmed via `docker inspect --format '{{.State.StartedAt}}'` that the container was never stopped, and `redis-cli GET` that the pre-existing key was untouched).

Setting up service-account access to a Shared Drive has two non-obvious prerequisites, both blockers the first time: the Drive API must be manually enabled on the service account's GCP project (off by default), and a service account has zero Drive storage quota of its own - it cannot write into a personal My Drive folder at all, even if shared to it as Editor (Google 403s with `storageQuotaExceeded`); it must be a real Shared Drive with the service account added as at least a Content Manager member.

---

**Repo Location:** `/home/kree/Tools/dockvault`
**Last Built:** 2026-08-17 (verified: `go build ./...`, `go vet ./...`, `go test ./...` (83 tests), `go test -race ./...`, `gofmt -l .`, `govulncheck` all clean; cross-compiles clean for linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64 (Windows additionally `go vet`-checked on its own, since its Credential Manager code is entirely excluded from a normal Linux `go test`/`go vet` run); real-container validation against postgres/redis/nginx via the actual CLI, not just mocks, including the durable-local-copy + integrity-verification feature's own real-infrastructure pass, a full pass against a real Google Shared Drive, a real `rabbitmq:3-management` backup/restore/tamper-refusal cycle against that same Shared Drive, a full `dockvault setup` run against that same real Shared Drive followed by a real backup through the remote it configured, and the missing-credential-prompt + connectivity-probe flow exercised against real postgres/mysql/redis/mongodb containers including the exit-0-on-auth-failure bug this testing caught and fixed)
