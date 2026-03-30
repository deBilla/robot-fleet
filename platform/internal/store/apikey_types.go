package store

import "time"

// APIKeyRecord represents an API key stored in the database.
type APIKeyRecord struct {
	KeyHash   string
	TenantID  string
	Name      string
	Role      string
	RateLimit int
	CreatedAt time.Time
	ExpiresAt *time.Time
	Revoked   bool
}
