package detector

import "strings"

// Service type identifiers. These strings are used as-is in job names,
// Google Drive paths, and generated script headers, so they're kept
// lowercase and stable.
const (
	ServicePostgres = "postgres"
	ServiceMySQL    = "mysql"
	ServiceMongoDB  = "mongodb"
	ServiceRedis    = "redis"
	ServiceN8N      = "n8n"
	ServiceRabbitmq = "rabbitmq"
	ServiceGeneric  = "generic"
	ServiceHostDir  = "hostdir"
)

// Recommended backup strategy identifiers, matched 1:1 against the
// executor names registered under internal/backup/.
const (
	BackupPgDump    = "pg_dump"
	BackupMysqldump = "mysqldump"
	BackupMongodump = "mongodump --archive"
	BackupBGSave    = "BGSAVE"
	BackupN8NLife   = "volume tar + container lifecycle"
	BackupTar       = "tar"
	// BackupRabbitmqDefinitions captures topology (exchanges, queues,
	// bindings, users, permissions, policies) via `rabbitmqctl
	// export_definitions`, not message bodies - see the rabbitmq
	// executor's doc comment for why raw data-directory copies are
	// deliberately not used here.
	BackupRabbitmqDefinitions = "rabbitmqctl export_definitions"
)

// imageMatchOrder is deliberately ordered: image name matching is a
// case-insensitive substring test, and this order determines the winner
// when an image name could plausibly match more than one entry (e.g. a
// custom image tagged "internal-mongo-postgres-proxy" - unlikely, but
// matching order should still be deterministic rather than map-iteration
// order, which Go does not guarantee).
var imageMatchOrder = []struct {
	substr  string
	service string
}{
	{"postgres", ServicePostgres},
	{"mysql", ServiceMySQL},
	{"mongo", ServiceMongoDB},
	{"redis", ServiceRedis},
	{"n8n", ServiceN8N},
	{"rabbitmq", ServiceRabbitmq},
}

// DetectServiceType infers a service type from a container image name
// using case-insensitive substring matching, per the spec's Phase 2 rules.
// Unmatched images are ServiceGeneric.
func DetectServiceType(imageName string) string {
	lower := strings.ToLower(imageName)
	for _, m := range imageMatchOrder {
		if strings.Contains(lower, m.substr) {
			return m.service
		}
	}
	return ServiceGeneric
}

// portServiceHints maps well-known ports to the service type they suggest.
// This is a confirmatory signal only (per spec Phase 2.3, "optional") -
// image name and env vars take precedence when they disagree.
var portServiceHints = map[int]string{
	5432:  ServicePostgres,
	3306:  ServiceMySQL,
	27017: ServiceMongoDB,
	6379:  ServiceRedis,
}

// DetectServiceTypeFromPort returns the service type a published port
// suggests, or "" if the port isn't one of the well-known ones dockvault
// recognizes.
func DetectServiceTypeFromPort(port int) string {
	return portServiceHints[port]
}

// RecommendedBackupType maps a service type to the backup strategy dockvault
// proposes for it, per the spec's Phase 3 table.
func RecommendedBackupType(serviceType string) string {
	switch serviceType {
	case ServicePostgres:
		return BackupPgDump
	case ServiceMySQL:
		return BackupMysqldump
	case ServiceMongoDB:
		return BackupMongodump
	case ServiceRedis:
		return BackupBGSave
	case ServiceN8N:
		return BackupN8NLife
	case ServiceRabbitmq:
		return BackupRabbitmqDefinitions
	case ServiceHostDir:
		return BackupTar
	default: // ServiceGeneric and anything unrecognized
		return BackupTar
	}
}

// credentialEnvKeys lists the specific env var names the spec calls out
// per service, in the order dockvault should prefer them when several are
// present (e.g. a *_USER before a generic *_PASSWORD).
var credentialEnvKeys = map[string][]string{
	ServicePostgres: {"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"},
	ServiceMySQL:    {"MYSQL_ROOT_PASSWORD", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"},
	ServiceMongoDB:  {"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD", "MONGO_INITDB_DATABASE"},
	ServiceRedis:    {"REDIS_PASSWORD"},
}

// CredentialEnvKeysFor exposes credentialEnvKeys to callers outside this
// package (the generate wizard's missing-credential prompt) - returns nil
// for any service type with no curated key list (e.g. n8n, rabbitmq),
// same as every internal lookup against the map already does.
func CredentialEnvKeysFor(serviceType string) []string {
	return credentialEnvKeys[serviceType]
}

// genericSecretMarkers matches the spec's catch-all: "any other key that
// looks like a secret". Checked as a substring against the env var name.
var genericSecretMarkers = []string{"PASSWORD", "TOKEN", "SECRET", "API_KEY"}
