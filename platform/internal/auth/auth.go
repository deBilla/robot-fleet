package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	TenantIDKey contextKey = "tenant_id"
	RoleKey     contextKey = "role"
)

// Roles
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
	RoleDev      = "developer"
)

// Claims extends JWT claims with tenant info.
type Claims struct {
	jwt.RegisteredClaims
	TenantID string `json:"tenant_id"`
	Role     string `json:"role"`
}

// TokenService handles JWT token creation and validation.
type TokenService struct {
	secret []byte
	issuer string
}

func NewTokenService(secret, issuer string) *TokenService {
	return &TokenService{secret: []byte(secret), issuer: issuer}
}

// GenerateToken creates a new JWT token for a tenant.
func (s *TokenService) GenerateToken(tenantID, role string, expiry time.Duration) (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   tenantID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		TenantID: tenantID,
		Role:     role,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ValidateToken parses and validates a JWT token.
func (s *TokenService) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// APIKeyStore provides API key validation backed by both hardcoded dev keys
// and an optional database lookup function for production keys.
type APIKeyStore struct {
	devKeys  map[string]*APIKeyInfo
	dbLookup func(ctx context.Context, keyHash string) (*APIKeyInfo, error) // optional DB-backed lookup
}

type APIKeyInfo struct {
	TenantID string
	Role     string
	Tier     string // pricing tier (free, pro, enterprise)
}

// NewAPIKeyStore creates a key store with hardcoded dev keys.
func NewAPIKeyStore() *APIKeyStore {
	return &APIKeyStore{
		devKeys: map[string]*APIKeyInfo{
			"dev-key-001": {TenantID: "tenant-dev", Role: RoleAdmin, Tier: "enterprise"},
			"dev-key-002": {TenantID: "tenant-demo", Role: RoleViewer, Tier: "free"},
		},
	}
}

// SetDBLookup configures a database-backed key lookup function.
// The function receives a SHA-256 hash of the API key and returns the key info.
func (s *APIKeyStore) SetDBLookup(fn func(ctx context.Context, keyHash string) (*APIKeyInfo, error)) {
	s.dbLookup = fn
}

func (s *APIKeyStore) Validate(apiKey string) (*APIKeyInfo, bool) {
	// Check hardcoded dev keys first (constant-time comparison)
	for key, info := range s.devKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1 {
			return info, true
		}
	}
	return nil, false
}

// ValidateWithContext checks dev keys then falls back to DB lookup.
func (s *APIKeyStore) ValidateWithContext(ctx context.Context, apiKey string) (*APIKeyInfo, bool) {
	// Check dev keys first
	if info, ok := s.Validate(apiKey); ok {
		return info, true
	}

	// Fall back to DB-backed lookup
	if s.dbLookup != nil {
		h := sha256Hash(apiKey)
		info, err := s.dbLookup(ctx, h)
		if err == nil && info != nil {
			return info, true
		}
	}
	return nil, false
}

func sha256Hash(s string) string {
	h := sha256Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// AuthMiddleware extracts and validates auth from requests.
// Supports: API key (X-API-Key header), JWT Bearer token, and OAuth2/OIDC Bearer token.
func AuthMiddleware(tokenSvc *TokenService, apiKeys *APIKeyStore, oidc ...*OIDCVerifier) func(http.Handler) http.Handler {
	var oidcVerifier *OIDCVerifier
	if len(oidc) > 0 {
		oidcVerifier = oidc[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try API key from header or query param (query param needed for WebSocket)
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				apiKey = r.URL.Query().Get("api_key")
			}
			if apiKey != "" {
				info, ok := apiKeys.ValidateWithContext(r.Context(), apiKey)
				if !ok {
					http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
					return
				}
				ctx := context.WithValue(r.Context(), TenantIDKey, info.TenantID)
				ctx = context.WithValue(ctx, RoleKey, info.Role)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Try Bearer token (JWT first, then OAuth2/OIDC fallback)
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

				// Try internal JWT
				claims, err := tokenSvc.ValidateToken(tokenStr)
				if err == nil {
					ctx := context.WithValue(r.Context(), TenantIDKey, claims.TenantID)
					ctx = context.WithValue(ctx, RoleKey, claims.Role)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}

				// Try OAuth2/OIDC token if verifier is configured
				if oidcVerifier != nil {
					claims, err := oidcVerifier.ValidateOAuth2Token(r.Context(), tokenStr)
					if err == nil {
						ctx := context.WithValue(r.Context(), TenantIDKey, claims.TenantID)
						ctx = context.WithValue(ctx, RoleKey, claims.Role)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}

				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			http.Error(w, `{"error":"missing authentication"}`, http.StatusUnauthorized)
		})
	}
}

// RequireRole middleware checks that the authenticated user has one of the allowed roles.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(RoleKey).(string)
			for _, allowed := range roles {
				if role == allowed {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		})
	}
}

// GetTenantID extracts tenant ID from context.
func GetTenantID(ctx context.Context) string {
	id, _ := ctx.Value(TenantIDKey).(string)
	return id
}
