package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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
	jwksKeys map[string]*rsa.PublicKey // kid → RSA public key (cached)
	jwksExp  time.Time
	cacheAge time.Duration
	client   *http.Client
}

// OpenIDConfiguration is the response from /.well-known/openid-configuration.
type OpenIDConfiguration struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// jwksResponse represents the JSON Web Key Set response.
type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

// jwkKey represents a single JSON Web Key.
type jwkKey struct {
	Kty string `json:"kty"` // Key type (RSA)
	Kid string `json:"kid"` // Key ID
	Use string `json:"use"` // Key usage (sig)
	Alg string `json:"alg"` // Algorithm (RS256)
	N   string `json:"n"`   // RSA modulus (base64url)
	E   string `json:"e"`   // RSA exponent (base64url)
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
		jwksKeys: make(map[string]*rsa.PublicKey),
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

// fetchJWKS fetches and caches the JSON Web Key Set from the issuer.
func (v *OIDCVerifier) fetchJWKS(ctx context.Context) error {
	v.mu.RLock()
	if time.Now().Before(v.jwksExp) && len(v.jwksKeys) > 0 {
		v.mu.RUnlock()
		return nil
	}
	jwksURL := v.jwksURL
	v.mu.RUnlock()

	if jwksURL == "" {
		return errors.New("JWKS URL not discovered; call DiscoverJWKS first")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	if err != nil {
		return fmt.Errorf("create JWKS request: %w", err)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, key := range jwks.Keys {
		if key.Kty != "RSA" || key.Use != "sig" {
			continue
		}
		pubKey, err := parseRSAPublicKey(key.N, key.E)
		if err != nil {
			slog.Warn("skipping invalid JWKS key", "kid", key.Kid, "error", err)
			continue
		}
		keys[key.Kid] = pubKey
	}

	v.mu.Lock()
	v.jwksKeys = keys
	v.jwksExp = time.Now().Add(v.cacheAge)
	v.mu.Unlock()

	slog.Info("JWKS keys refreshed", "count", len(keys))
	return nil
}

// getKey returns the RSA public key for a given key ID.
func (v *OIDCVerifier) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if err := v.fetchJWKS(ctx); err != nil {
		return nil, err
	}

	v.mu.RLock()
	key, ok := v.jwksKeys[kid]
	v.mu.RUnlock()

	if !ok {
		// Key not found — force refresh in case keys were rotated
		v.mu.Lock()
		v.jwksExp = time.Time{} // invalidate cache
		v.mu.Unlock()

		if err := v.fetchJWKS(ctx); err != nil {
			return nil, err
		}

		v.mu.RLock()
		key, ok = v.jwksKeys[kid]
		v.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("key ID %q not found in JWKS", kid)
		}
	}

	return key, nil
}

// ValidateOAuth2Token validates a token by verifying its cryptographic signature
// against the issuer's JWKS public keys, then checking claims.
func (v *OIDCVerifier) ValidateOAuth2Token(ctx context.Context, tokenStr string) (*Claims, error) {
	// Parse JWT header to extract kid
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token: expected 3 parts")
	}

	headerJSON, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode token header: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse token header: %w", err)
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s (only RS256 supported)", header.Alg)
	}

	// Fetch the public key for this kid
	pubKey, err := v.getKey(ctx, header.Kid)
	if err != nil {
		return nil, fmt.Errorf("get signing key: %w", err)
	}

	// Verify signature: decode signature, hash header.payload, verify with RSA
	signingInput := parts[0] + "." + parts[1]
	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode token signature: %w", err)
	}

	if err := verifyRS256(pubKey, []byte(signingInput), signature); err != nil {
		return nil, fmt.Errorf("invalid token signature: %w", err)
	}

	// Signature verified — now validate claims
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

	if rawClaims.Iss != v.issuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", v.issuer, rawClaims.Iss)
	}

	if v.clientID != "" && !audienceContains(rawClaims.Aud, v.clientID) {
		return nil, fmt.Errorf("invalid audience: expected %s", v.clientID)
	}

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

// parseRSAPublicKey constructs an RSA public key from base64url-encoded modulus and exponent.
func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}

// verifyRS256 verifies an RS256 (RSASSA-PKCS1-v1_5 with SHA-256) signature.
func verifyRS256(key *rsa.PublicKey, signingInput, signature []byte) error {
	hash := sha256.Sum256(signingInput)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature)
}
