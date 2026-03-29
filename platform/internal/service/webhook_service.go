package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// WebhookService manages webhook registration and event dispatch.
type WebhookService interface {
	Register(ctx context.Context, tenantID, url string, events []string, secret string) (*store.WebhookRecord, error)
	List(ctx context.Context, tenantID string) ([]*store.WebhookRecord, error)
	Delete(ctx context.Context, tenantID, id string) error
	Dispatch(ctx context.Context, eventType string, payload any) error
}

type webhookService struct {
	repo   store.WebhookRepository
	client *http.Client
}

// NewWebhookService creates a new webhook service.
func NewWebhookService(repo store.WebhookRepository) WebhookService {
	return &webhookService{
		repo:   repo,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *webhookService) Register(ctx context.Context, tenantID, url string, events []string, secret string) (*store.WebhookRecord, error) {
	id := fmt.Sprintf("wh-%d", time.Now().UnixNano())
	rec := &store.WebhookRecord{
		ID:        id,
		TenantID:  tenantID,
		URL:       url,
		Events:    events,
		Secret:    secret,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.CreateWebhook(ctx, rec); err != nil {
		return nil, fmt.Errorf("register webhook: %w", err)
	}
	return rec, nil
}

func (s *webhookService) List(ctx context.Context, tenantID string) ([]*store.WebhookRecord, error) {
	return s.repo.ListWebhooks(ctx, tenantID)
}

func (s *webhookService) Delete(ctx context.Context, tenantID, id string) error {
	return s.repo.DeleteWebhook(ctx, tenantID, id)
}

// Dispatch sends an event to all webhooks subscribed to the event type.
func (s *webhookService) Dispatch(ctx context.Context, eventType string, payload any) error {
	hooks, err := s.repo.ListWebhooksByEvent(ctx, eventType)
	if err != nil {
		return fmt.Errorf("list webhooks for event %s: %w", eventType, err)
	}

	body, err := json.Marshal(map[string]any{
		"event":     eventType,
		"payload":   payload,
		"timestamp": time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	for _, hook := range hooks {
		go s.deliver(hook, eventType, body)
	}
	return nil
}

const maxWebhookRetries = 3

func (s *webhookService) deliver(hook *store.WebhookRecord, eventType string, body []byte) {
	for attempt := range maxWebhookRetries {
		req, err := http.NewRequest("POST", hook.URL, bytes.NewReader(body))
		if err != nil {
			slog.Error("webhook request creation failed", "webhook", hook.ID, "error", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FleetOS-Event", eventType)

		if hook.Secret != "" {
			mac := hmac.New(sha256.New, []byte(hook.Secret))
			mac.Write(body)
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-FleetOS-Signature", sig)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			slog.Warn("webhook delivery failed", "webhook", hook.ID, "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second) // exponential backoff
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			slog.Debug("webhook delivered", "webhook", hook.ID, "event", eventType)
			return
		}

		slog.Warn("webhook returned non-2xx", "webhook", hook.ID, "status", resp.StatusCode, "attempt", attempt+1)
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}

	slog.Error("webhook delivery exhausted retries", "webhook", hook.ID, "event", eventType)
}
