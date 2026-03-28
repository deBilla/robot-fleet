package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func makeTestToken(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s.%s.fake-signature", header, payloadB64)
}

func TestOIDCVerifier_ValidToken(t *testing.T) {
	v, err := NewOIDCVerifier(OIDCConfig{
		IssuerURL: "https://auth.fleetos.io",
		ClientID:  "fleetos-api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	token := makeTestToken(map[string]any{
		"sub":       "user-123",
		"iss":       "https://auth.fleetos.io",
		"aud":       "fleetos-api",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"tenant_id": "tenant-dev",
		"role":      "admin",
	})

	claims, err := v.ValidateOAuth2Token(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims.TenantID != "tenant-dev" {
		t.Errorf("expected tenant-dev, got %s", claims.TenantID)
	}
	if claims.Role != "admin" {
		t.Errorf("expected admin, got %s", claims.Role)
	}
}

func TestOIDCVerifier_ExpiredToken(t *testing.T) {
	v, _ := NewOIDCVerifier(OIDCConfig{IssuerURL: "https://auth.fleetos.io"})

	token := makeTestToken(map[string]any{
		"iss": "https://auth.fleetos.io",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	_, err := v.ValidateOAuth2Token(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestOIDCVerifier_WrongIssuer(t *testing.T) {
	v, _ := NewOIDCVerifier(OIDCConfig{IssuerURL: "https://auth.fleetos.io"})

	token := makeTestToken(map[string]any{
		"iss": "https://evil.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := v.ValidateOAuth2Token(token)
	if err == nil {
		t.Error("expected error for wrong issuer")
	}
}

func TestOIDCVerifier_WrongAudience(t *testing.T) {
	v, _ := NewOIDCVerifier(OIDCConfig{
		IssuerURL: "https://auth.fleetos.io",
		ClientID:  "fleetos-api",
	})

	token := makeTestToken(map[string]any{
		"iss": "https://auth.fleetos.io",
		"aud": "other-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := v.ValidateOAuth2Token(token)
	if err == nil {
		t.Error("expected error for wrong audience")
	}
}

func TestOIDCVerifier_MalformedToken(t *testing.T) {
	v, _ := NewOIDCVerifier(OIDCConfig{IssuerURL: "https://auth.fleetos.io"})

	_, err := v.ValidateOAuth2Token("not-a-jwt")
	if err == nil {
		t.Error("expected error for malformed token")
	}
}

func TestOIDCVerifier_ArrayAudience(t *testing.T) {
	v, _ := NewOIDCVerifier(OIDCConfig{
		IssuerURL: "https://auth.fleetos.io",
		ClientID:  "fleetos-api",
	})

	token := makeTestToken(map[string]any{
		"iss": "https://auth.fleetos.io",
		"aud": []string{"other-client", "fleetos-api"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	claims, err := v.ValidateOAuth2Token(token)
	if err != nil {
		t.Fatalf("expected success with array audience, got: %v", err)
	}
	if claims == nil {
		t.Error("expected non-nil claims")
	}
}

func TestNewOIDCVerifier_EmptyIssuer(t *testing.T) {
	_, err := NewOIDCVerifier(OIDCConfig{})
	if err == nil {
		t.Error("expected error for empty issuer")
	}
}
