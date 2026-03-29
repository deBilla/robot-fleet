package activities

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.temporal.io/sdk/temporal"
)

// WebhookActivities holds dependencies for webhook delivery Temporal activities.
type WebhookActivities struct {
	Client *http.Client
}

// NewWebhookActivities creates webhook activities with a default HTTP client.
func NewWebhookActivities() *WebhookActivities {
	return &WebhookActivities{
		Client: &http.Client{Timeout: 10 * time.Second},
	}
}

// DeliverWebhookInput is the input for DeliverWebhook activity.
type DeliverWebhookInput struct {
	WebhookID string `json:"webhook_id"`
	URL       string `json:"url"`
	Secret    string `json:"secret"`
	EventType string `json:"event_type"`
	Body      []byte `json:"body"`
}

// DeliverWebhook sends an HTTP POST to a webhook endpoint.
// Returns a non-retryable error for 4xx (permanent), retryable for 5xx/network.
func (a *WebhookActivities) DeliverWebhook(ctx context.Context, input DeliverWebhookInput) error {
	req, err := http.NewRequestWithContext(ctx, "POST", input.URL, bytes.NewReader(input.Body))
	if err != nil {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("invalid webhook URL: %s", input.URL), "PermanentWebhookError", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FleetOS-Event", input.EventType)

	if input.Secret != "" {
		mac := hmac.New(sha256.New, []byte(input.Secret))
		mac.Write(input.Body)
		req.Header.Set("X-FleetOS-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook delivery failed: %w", err) // retryable
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("webhook returned %d", resp.StatusCode), "PermanentWebhookError", nil)
	}

	return fmt.Errorf("webhook returned %d", resp.StatusCode) // retryable (5xx)
}
