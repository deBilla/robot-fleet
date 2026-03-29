package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testKeyPair holds a generated RSA key pair for tests.
var testKeyPair *rsa.PrivateKey

func init() {
	var err error
	testKeyPair, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
}

// signTestToken creates a properly RS256-signed JWT for testing.
func signTestToken(kid string, claims map[string]any) string {
	headerMap := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	headerJSON, _ := json.Marshal(headerMap)
	header := base64.RawURLEncoding.EncodeToString(headerJSON)

	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)

	signingInput := header + "." + payloadB64
	hash := sha256.Sum256([]byte(signingInput))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, testKeyPair, crypto.SHA256, hash[:])
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

// startJWKSServer starts a test HTTP server serving JWKS and OIDC discovery.
func startJWKSServer(t *testing.T, key *rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// JWKS endpoint
	nBytes := key.N.Bytes()
	eBytes := big.NewInt(int64(key.E)).Bytes()
	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(nBytes),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Will be replaced after server starts
	}))

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":   srv.URL,
			"jwks_uri": srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jwks)
	})

	srv.Config.Handler = mux
	return srv
}

// makeTestTokenUnsigned creates a fake unsigned token (for negative tests).
func makeTestTokenUnsigned(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT","kid":"test-kid"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s.%s.fake-signature", header, payloadB64)
}

func setupVerifier(t *testing.T) (*OIDCVerifier, *httptest.Server) {
	t.Helper()
	srv := startJWKSServer(t, &testKeyPair.PublicKey, "test-kid")

	v, err := NewOIDCVerifier(OIDCConfig{
		IssuerURL: srv.URL,
		ClientID:  "fleetos-api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := v.DiscoverJWKS(context.Background()); err != nil {
		t.Fatalf("JWKS discovery failed: %v", err)
	}

	return v, srv
}

func makeValidSignedToken(issuer string) string {
	return signTestToken("test-kid", map[string]any{
		"sub":       "user-123",
		"iss":       issuer,
		"aud":       "fleetos-api",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"tenant_id": "tenant-dev",
		"role":      "admin",
	})
}

func TestOIDCVerifier_ValidSignedToken(t *testing.T) {
	v, srv := setupVerifier(t)
	defer srv.Close()

	token := makeValidSignedToken(srv.URL)
	claims, err := v.ValidateOAuth2Token(context.Background(), token)
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

func TestOIDCVerifier_InvalidSignature(t *testing.T) {
	v, srv := setupVerifier(t)
	defer srv.Close()

	// Token with fake signature should be rejected
	token := makeTestTokenUnsigned(map[string]any{
		"iss":       srv.URL,
		"aud":       "fleetos-api",
		"exp":       time.Now().Add(time.Hour).Unix(),
		"tenant_id": "tenant-dev",
		"role":      "admin",
	})

	_, err := v.ValidateOAuth2Token(context.Background(), token)
	if err == nil {
		t.Error("expected error for token with invalid signature")
	}
}

func TestOIDCVerifier_ExpiredToken(t *testing.T) {
	v, srv := setupVerifier(t)
	defer srv.Close()

	token := signTestToken("test-kid", map[string]any{
		"iss": srv.URL,
		"aud": "fleetos-api",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	_, err := v.ValidateOAuth2Token(context.Background(), token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestOIDCVerifier_WrongIssuer(t *testing.T) {
	v, srv := setupVerifier(t)
	defer srv.Close()

	token := signTestToken("test-kid", map[string]any{
		"iss": "https://evil.com",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := v.ValidateOAuth2Token(context.Background(), token)
	if err == nil {
		t.Error("expected error for wrong issuer")
	}
}

func TestOIDCVerifier_MalformedToken(t *testing.T) {
	v, srv := setupVerifier(t)
	defer srv.Close()

	_, err := v.ValidateOAuth2Token(context.Background(), "not-a-jwt")
	if err == nil {
		t.Error("expected error for malformed token")
	}
}

func TestNewOIDCVerifier_EmptyIssuer(t *testing.T) {
	_, err := NewOIDCVerifier(OIDCConfig{})
	if err == nil {
		t.Error("expected error for empty issuer")
	}
}
