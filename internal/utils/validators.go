package utils

import (
	"fmt"
	"regexp"
)

// jobNameRE matches the sanitized job-name shape dockvault generates and
// requires for user-supplied names too, so job directories are always
// safe path components.
var jobNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

// ValidateJobName reports whether name is safe to use as a job directory
// name.
func ValidateJobName(name string) error {
	if name == "" {
		return fmt.Errorf("job name cannot be empty")
	}
	if !jobNameRE.MatchString(name) {
		return fmt.Errorf("job name %q must be lowercase alphanumeric with underscores, starting with a letter or digit", name)
	}
	return nil
}

// ValidateRetentionDays reports whether days is a sane retention window.
func ValidateRetentionDays(days int) error {
	if days < 1 {
		return fmt.Errorf("retention_days must be at least 1, got %d", days)
	}
	return nil
}

// ValidateMinFilesToKeep reports whether n is a sane minimum file count.
func ValidateMinFilesToKeep(n int) error {
	if n < 0 {
		return fmt.Errorf("min_files_to_keep cannot be negative, got %d", n)
	}
	return nil
}

// ValidateLocalRetentionCount reports whether n is a sane local-copy
// retention count. 0 (or negative) is valid and means "disable local
// retention entirely" (see workspace.Config.LocalRetentionCount's doc
// comment) - only a value that couldn't possibly mean that is rejected.
func ValidateLocalRetentionCount(n int) error {
	if n < 0 {
		return fmt.Errorf("local_retention_count cannot be negative, got %d", n)
	}
	return nil
}

// ValidateWebhookURL is a light sanity check (http/https prefix), not full
// URL validation - the webhook call itself will surface a clearer error if
// the URL doesn't actually work.
var webhookURLRE = regexp.MustCompile(`^https?://`)

func ValidateWebhookURL(url string) error {
	if url == "" {
		return nil // optional field
	}
	if !webhookURLRE.MatchString(url) {
		return fmt.Errorf("webhook URL %q must start with http:// or https://", url)
	}
	return nil
}
