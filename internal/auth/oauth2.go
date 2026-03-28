package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OIDCConfig holds OpenID Connect configuration.
type OIDCConfig struct {
	IssuerURL    string // e.g., "https://accounts.google.com"
	ClientID     string // The OAuth2 client ID (audience)
	JWKSCacheAge time.Duration
}

// OIDCVerifier validates OAuth2/OIDC tokens by fetching JWKS from the issuer.
type OIDCVerifier struct {
	issuer   string
	clientID string
	jwksURL  string

	mu       sync.RWMutex
	jwksKeys map[string]any // kid → key (cached)
	jwksExp  time.Time
	cacheAge time.Duration
	client   *http.Client
}

// OpenIDConfiguration is the response from /.well-known/openid-configuration.
type OpenIDConfiguration struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// NewOIDCVerifier creates a new OIDC token verifier.
// It discovers the JWKS endpoint from the issuer's .well-known/openid-configuration.
func NewOIDCVerifier(cfg OIDCConfig) (*OIDCVerifier, error) {
	if cfg.IssuerURL == "" {
		return nil, errors.New("issuer URL is required")
	}
	cacheAge := cfg.JWKSCacheAge
	if cacheAge == 0 {
		cacheAge = 1 * time.Hour
	}
	return &OIDCVerifier{
		issuer:   strings.TrimRight(cfg.IssuerURL, "/"),
		clientID: cfg.ClientID,
		jwksKeys: make(map[string]any),
		cacheAge: cacheAge,
		client:   &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// DiscoverJWKS fetches the OIDC discovery document and extracts the JWKS URI.
func (v *OIDCVerifier) DiscoverJWKS(ctx context.Context) error {
	wellKnownURL := v.issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, "GET", wellKnownURL, nil)
	if err != nil {
		return fmt.Errorf("create discovery request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}

	var config OpenIDConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return fmt.Errorf("parse OIDC discovery: %w", err)
	}

	if config.Issuer != v.issuer {
		return fmt.Errorf("issuer mismatch: expected %s, got %s", v.issuer, config.Issuer)
	}

	v.mu.Lock()
	v.jwksURL = config.JWKSURI
	v.mu.Unlock()

	slog.Info("OIDC discovery complete", "issuer", v.issuer, "jwks_uri", config.JWKSURI)
	return nil
}

// ValidateOAuth2Token validates a token by checking its structure and claims.
// WARNING: This validates claims (issuer, audience, expiry) but does NOT verify
// the JWT signature. In production, call DiscoverJWKS() first and verify the
// signature against the issuer's public keys (JWKS). Without signature verification,
// any client can forge tokens with valid-looking claims.
func (v *OIDCVerifier) ValidateOAuth2Token(tokenStr string) (*Claims, error) {
	// Parse JWT parts (header.payload.signature)
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token: expected 3 parts")
	}

	// Decode payload (base64url)
	payload, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode token payload: %w", err)
	}

	var rawClaims struct {
		Sub      string `json:"sub"`
		Iss      string `json:"iss"`
		Aud      any    `json:"aud"`
		Exp      int64  `json:"exp"`
		TenantID string `json:"tenant_id"`
		Role     string `json:"role"`
	}
	if err := json.Unmarshal(payload, &rawClaims); err != nil {
		return nil, fmt.Errorf("parse token claims: %w", err)
	}

	// Validate issuer
	if rawClaims.Iss != v.issuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", v.issuer, rawClaims.Iss)
	}

	// Validate audience
	if v.clientID != "" && !audienceContains(rawClaims.Aud, v.clientID) {
		return nil, fmt.Errorf("invalid audience: expected %s", v.clientID)
	}

	// Validate expiration
	if rawClaims.Exp > 0 && time.Unix(rawClaims.Exp, 0).Before(time.Now()) {
		return nil, errors.New("token expired")
	}

	return &Claims{
		TenantID: rawClaims.TenantID,
		Role:     rawClaims.Role,
	}, nil
}

// audienceContains checks if the audience (string or []string) contains the expected value.
func audienceContains(aud any, expected string) bool {
	switch v := aud.(type) {
	case string:
		return v == expected
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}

// decodeBase64URL decodes a base64url-encoded string (no padding).
func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
