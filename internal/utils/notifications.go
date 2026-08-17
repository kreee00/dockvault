package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookPayload is the JSON body POSTed to a job's webhook URL on backup
// completion or error. The field names are generic on purpose so the same
// payload is usable as-is by Slack (via a formatter step), Discord, or an
// n8n workflow trigger.
type WebhookPayload struct {
	Job       string    `json:"job"`
	Status    string    `json:"status"` // "success" | "failure"
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	Elapsed   string    `json:"elapsed,omitempty"`
}

// SendWebhook POSTs payload as JSON to url. Failures are returned, not
// swallowed - callers should log-and-continue rather than fail the backup
// job over a notification error.
func SendWebhook(ctx context.Context, url string, payload WebhookPayload) error {
	if url == "" {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
