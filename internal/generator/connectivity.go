package generator

import (
	"bytes"
	"context"
	"fmt"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
)

// testConnectivity runs a lightweight per-service-type reachability probe
// against job's container via docker exec, after Wizard.
// PromptMissingCredentials has collected whatever credentials it could.
// ok reports success; msg is a human-readable summary either way, or ""
// if job.ServiceType has no defined probe - defensive only, since
// PromptMissingCredentials already skips calling this for any type with
// no detector.CredentialEnvKeysFor entry.
//
// mysql and redis check the command's OUTPUT, not just its exit code -
// confirmed for real (not assumed) against live containers: `mysqladmin
// ping` and `redis-cli ping` both exit 0 even on an authentication
// failure, printing "Access denied"/"NOAUTH" to stdout instead of
// failing the process. Trusting `err == nil` alone would silently report
// "connected" for a wrong password on either one.
func testConnectivity(ctx context.Context, dc backup.DockerClient, job *backup.Job) (ok bool, msg string) {
	switch job.ServiceType {
	case detector.ServicePostgres:
		// pg_isready only checks whether the postmaster is accepting
		// connections at all - it never touches the given user/db, so it
		// can't distinguish a working job from a typo'd database name
		// (confirmed for real: it reports "accepting connections" even
		// for a nonexistent db/user). `psql -c "SELECT 1"` at least
		// proves the role and database both exist.
		//
		// KNOWN LIMITATION, not fixable from a docker-exec probe: the
		// official postgres image's default pg_hba.conf trusts every
		// local connection (the Unix socket AND 127.0.0.1/::1 over TCP)
		// unconditionally, regardless of password - confirmed for real
		// against a live container (a deliberately wrong PGPASSWORD
		// still connects and runs the query). A docker-exec-based probe
		// - and the postgres backup executor's own pg_dump, for that
		// matter - can therefore only ever confirm the user/database
		// names are right, not that the password is; there's no way to
		// check that without connecting from outside the container.
		env := map[string]string{}
		if pw := job.Credentials["POSTGRES_PASSWORD"]; pw != "" {
			env["PGPASSWORD"] = pw
		}
		out, err := dc.Exec(ctx, job.ContainerName, []string{"psql", "-U", job.Credentials["POSTGRES_USER"], "-d", job.Credentials["POSTGRES_DB"], "-c", "SELECT 1"}, env, nil)
		if err != nil {
			return false, connectivityFailMsg(out, err)
		}
		return true, "confirmed the postgres user/database exist (this image's default auth trusts local connections regardless of password - can't check that part)"

	case detector.ServiceMySQL:
		env := map[string]string{"MYSQL_PWD": job.Credentials["MYSQL_ROOT_PASSWORD"]}
		out, err := dc.Exec(ctx, job.ContainerName, []string{"mysqladmin", "ping", "-u", "root"}, env, nil)
		if err != nil || !bytes.Contains(out, []byte("mysqld is alive")) {
			return false, connectivityFailMsg(out, err)
		}
		return true, "reached mysql"

	case detector.ServiceMongoDB:
		user := job.Credentials["MONGO_INITDB_ROOT_USERNAME"]
		pw := job.Credentials["MONGO_INITDB_ROOT_PASSWORD"]
		cmd := []string{"mongosh", "--quiet", "--eval", "db.adminCommand({ping:1})"}
		if user != "" {
			cmd = append(cmd, "-u", user, "-p", pw, "--authenticationDatabase", "admin")
		}
		out, err := dc.Exec(ctx, job.ContainerName, cmd, nil, nil)
		if err != nil {
			// Older mongo images ship `mongo`, not `mongosh` - retry with
			// the legacy shell before giving up.
			cmd[0] = "mongo"
			out, err = dc.Exec(ctx, job.ContainerName, cmd, nil, nil)
		}
		if err != nil {
			return false, connectivityFailMsg(out, err)
		}
		return true, "reached mongodb"

	case detector.ServiceRedis:
		cmd := []string{"redis-cli", "ping"}
		if pw := job.Credentials["REDIS_PASSWORD"]; pw != "" {
			cmd = []string{"redis-cli", "-a", pw, "--no-auth-warning", "ping"}
		}
		out, err := dc.Exec(ctx, job.ContainerName, cmd, nil, nil)
		if err != nil || !bytes.Contains(out, []byte("PONG")) {
			return false, connectivityFailMsg(out, err)
		}
		return true, "reached redis"

	default:
		return true, ""
	}
}

// connectivityFailMsg reports either the docker-exec-level error (the
// process itself failed/couldn't run) or, when the process exited 0 but
// its output didn't contain what a real success looks like (see this
// file's doc comment - mysqladmin/redis-cli both do this on auth
// failure), a trimmed excerpt of that output instead.
func connectivityFailMsg(out []byte, err error) string {
	if err != nil {
		return fmt.Sprintf("failed (%v)", err)
	}
	return fmt.Sprintf("failed (%s)", bytes.TrimSpace(out))
}
