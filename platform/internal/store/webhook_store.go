package store

import (
	"context"
	"fmt"
)

func (s *PostgresStore) CreateWebhook(ctx context.Context, w *WebhookRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO webhooks (id, tenant_id, url, events, secret, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, w.ID, w.TenantID, w.URL, w.Events, w.Secret, w.Active, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("create webhook %s: %w", w.ID, err)
	}
	return nil
}

func (s *PostgresStore) ListWebhooks(ctx context.Context, tenantID string) ([]*WebhookRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, url, events, active, created_at
		FROM webhooks WHERE tenant_id = $1 AND active = TRUE
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks for tenant %s: %w", tenantID, err)
	}
	defer rows.Close()

	var hooks []*WebhookRecord
	for rows.Next() {
		h := &WebhookRecord{}
		if err := rows.Scan(&h.ID, &h.TenantID, &h.URL, &h.Events, &h.Active, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan webhook row: %w", err)
		}
		hooks = append(hooks, h)
	}
	return hooks, rows.Err()
}

func (s *PostgresStore) ListWebhooksByEvent(ctx context.Context, eventType string) ([]*WebhookRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, url, events, secret, active, created_at
		FROM webhooks WHERE active = TRUE AND $1 = ANY(events)
	`, eventType)
	if err != nil {
		return nil, fmt.Errorf("list webhooks for event %s: %w", eventType, err)
	}
	defer rows.Close()

	var hooks []*WebhookRecord
	for rows.Next() {
		h := &WebhookRecord{}
		if err := rows.Scan(&h.ID, &h.TenantID, &h.URL, &h.Events, &h.Secret, &h.Active, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan webhook row: %w", err)
		}
		hooks = append(hooks, h)
	}
	return hooks, rows.Err()
}

func (s *PostgresStore) DeleteWebhook(ctx context.Context, tenantID, id string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE webhooks SET active = FALSE WHERE id = $1 AND tenant_id = $2
	`, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete webhook %s: %w", id, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("webhook %s not found", id)
	}
	return nil
}

var _ WebhookRepository = (*PostgresStore)(nil)
