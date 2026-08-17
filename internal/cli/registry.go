package cli

import (
	"dockvault/internal/backup"
	"dockvault/internal/backup/hostdir"
	"dockvault/internal/backup/mongodb"
	"dockvault/internal/backup/mysql"
	"dockvault/internal/backup/n8n"
	"dockvault/internal/backup/postgres"
	"dockvault/internal/backup/rabbitmq"
	"dockvault/internal/backup/redis"
	"dockvault/internal/backup/standard"
	"dockvault/internal/restore"
	"dockvault/internal/workspace"
)

// buildBackupRegistry wires every backup executor package to dc/rc - the
// same two clients, shared across all eight, since none of them hold
// per-executor state.
func buildBackupRegistry(dc backup.DockerClient, rc backup.RcloneClient) backup.Registry {
	return backup.NewRegistry(
		standard.New(dc, rc),
		postgres.New(dc, rc),
		mysql.New(dc, rc),
		mongodb.New(dc, rc),
		redis.New(dc, rc),
		n8n.New(dc, rc),
		hostdir.New(dc, rc),
		rabbitmq.New(dc, rc),
	)
}

// buildRestoreRegistry is buildBackupRegistry's counterpart for restore
// executors.
func buildRestoreRegistry(dc backup.DockerClient, rc backup.RcloneClient) restore.Registry {
	return restore.NewRegistry(
		restore.NewStandardRestorer(dc, rc),
		restore.NewPostgresRestorer(dc, rc),
		restore.NewMysqlRestorer(dc, rc),
		restore.NewMongodbRestorer(dc, rc),
		restore.NewRedisRestorer(dc, rc),
		restore.NewN8nRestorer(dc, rc),
		restore.NewHostdirRestorer(dc, rc),
		restore.NewRabbitmqRestorer(dc, rc),
	)
}

// resolveRcloneRemote decides the rclone remote name to use: an explicit
// --rclone-remote flag wins, then config.json's rclone_remote, then the
// built-in default. flag.FlagSet doesn't cheaply expose "was this flag
// explicitly passed", so this compares against the hardcoded default
// (also defaultGlobal's value) as a proxy for "the user didn't override
// it" - good enough for a single well-known default string.
func resolveRcloneRemote(g Global, cfg workspace.Config) string {
	const builtinDefault = "dockvault_backup"
	if g.RcloneRemote != "" && g.RcloneRemote != builtinDefault {
		return g.RcloneRemote
	}
	if cfg.RcloneRemote != "" {
		return cfg.RcloneRemote
	}
	return g.RcloneRemote
}
