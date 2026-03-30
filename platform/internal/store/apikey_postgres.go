package store

import (
	"context"
	"fmt"
)

// CreateAPIKey stores a new API key (hashed) in the database.
func (s *PostgresStore) CreateAPIKey(ctx context.Context, k *APIKeyRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO api_keys (key_hash, tenant_id, name, role, rate_limit, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, k.KeyHash, k.TenantID, k.Name, k.Role, k.RateLimit, k.CreatedAt, k.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create api key: %w", err)
	}
	return nil
}

// GetAPIKey retrieves a non-revoked, non-expired API key by its hash.
// Used by auth middleware for key validation.
func (s *PostgresStore) GetAPIKey(ctx context.Context, keyHash string) (*APIKeyRecord, error) {
	var k APIKeyRecord
	err := s.pool.QueryRow(ctx, `
		SELECT key_hash, tenant_id, name, role, rate_limit, created_at, expires_at, revoked
		FROM api_keys
		WHERE key_hash = $1
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, keyHash).Scan(&k.KeyHash, &k.TenantID, &k.Name, &k.Role, &k.RateLimit, &k.CreatedAt, &k.ExpiresAt, &k.Revoked)
	if err != nil {
		return nil, fmt.Errorf("get api key: %w", err)
	}
	return &k, nil
}

// ListAPIKeys returns all API keys for a tenant, including revoked ones.
// Used by admin interface for key management visibility.
func (s *PostgresStore) ListAPIKeys(ctx context.Context, tenantID string) ([]*APIKeyRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT key_hash, tenant_id, name, role, rate_limit, created_at, expires_at, revoked
		FROM api_keys
		WHERE tenant_id = $1
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []*APIKeyRecord
	for rows.Next() {
		var k APIKeyRecord
		if err := rows.Scan(&k.KeyHash, &k.TenantID, &k.Name, &k.Role, &k.RateLimit, &k.CreatedAt, &k.ExpiresAt, &k.Revoked); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		keys = append(keys, &k)
	}
	return keys, nil
}

// RevokeAPIKey marks an API key as revoked.
func (s *PostgresStore) RevokeAPIKey(ctx context.Context, keyHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked = TRUE WHERE key_hash = $1`, keyHash)
	if err != nil {
		return fmt.Errorf("revoke api key %s: %w", keyHash, err)
	}
	return nil
}
