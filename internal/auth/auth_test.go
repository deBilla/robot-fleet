package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenService_GenerateAndValidate(t *testing.T) {
	svc := NewTokenService("test-secret", "test-issuer")

	token, err := svc.GenerateToken("tenant-1", RoleAdmin, time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("failed to validate token: %v", err)
	}
	if claims.TenantID != "tenant-1" {
		t.Errorf("expected tenant-1, got %s", claims.TenantID)
	}
	if claims.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", claims.Role)
	}
}

func TestTokenService_ExpiredToken(t *testing.T) {
	svc := NewTokenService("test-secret", "test-issuer")

	token, err := svc.GenerateToken("tenant-1", RoleAdmin, -time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	_, err = svc.ValidateToken(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestTokenService_InvalidSecret(t *testing.T) {
	svc1 := NewTokenService("secret-1", "test-issuer")
	svc2 := NewTokenService("secret-2", "test-issuer")

	token, _ := svc1.GenerateToken("tenant-1", RoleAdmin, time.Hour)

	_, err := svc2.ValidateToken(token)
	if err == nil {
		t.Error("expected error for token signed with different secret")
	}
}

func TestTokenService_InvalidTokenString(t *testing.T) {
	svc := NewTokenService("test-secret", "test-issuer")

	_, err := svc.ValidateToken("not-a-valid-token")
	if err == nil {
		t.Error("expected error for invalid token string")
	}
}

func TestTokenService_DifferentRoles(t *testing.T) {
	svc := NewTokenService("test-secret", "test-issuer")

	roles := []string{RoleAdmin, RoleOperator, RoleViewer, RoleDev}
	for _, role := range roles {
		token, err := svc.GenerateToken("tenant-1", role, time.Hour)
		if err != nil {
			t.Fatalf("failed to generate token for role %s: %v", role, err)
		}
		claims, err := svc.ValidateToken(token)
		if err != nil {
			t.Fatalf("failed to validate token for role %s: %v", role, err)
		}
		if claims.Role != role {
			t.Errorf("expected role %s, got %s", role, claims.Role)
		}
	}
}

func TestAPIKeyStore_ValidKey(t *testing.T) {
	store := NewAPIKeyStore()

	info, ok := store.Validate("dev-key-001")
	if !ok {
		t.Fatal("expected valid key")
	}
	if info.TenantID != "tenant-dev" {
		t.Errorf("expected tenant-dev, got %s", info.TenantID)
	}
	if info.Role != RoleAdmin {
		t.Errorf("expected admin role, got %s", info.Role)
	}
}

func TestAPIKeyStore_InvalidKey(t *testing.T) {
	store := NewAPIKeyStore()

	_, ok := store.Validate("invalid-key")
	if ok {
		t.Error("expected invalid key to fail validation")
	}
}

func TestAPIKeyStore_SecondKey(t *testing.T) {
	store := NewAPIKeyStore()

	info, ok := store.Validate("dev-key-002")
	if !ok {
		t.Fatal("expected valid key")
	}
	if info.TenantID != "tenant-demo" {
		t.Errorf("expected tenant-demo, got %s", info.TenantID)
	}
	if info.Role != RoleViewer {
		t.Errorf("expected viewer role, got %s", info.Role)
	}
}

func TestAPIKeyStore_EmptyKey(t *testing.T) {
	store := NewAPIKeyStore()

	_, ok := store.Validate("")
	if ok {
		t.Error("empty key should not be valid")
	}
}

func TestAuthMiddleware_WithAPIKey(t *testing.T) {
	tokenSvc := NewTokenService("test-secret", "test-issuer")
	apiKeys := NewAPIKeyStore()

	handler := AuthMiddleware(tokenSvc, apiKeys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := GetTenantID(r.Context())
		if tenantID != "tenant-dev" {
			t.Errorf("expected tenant-dev, got %s", tenantID)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "dev-key-001")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_WithBearerToken(t *testing.T) {
	tokenSvc := NewTokenService("test-secret", "test-issuer")
	apiKeys := NewAPIKeyStore()
	token, _ := tokenSvc.GenerateToken("tenant-jwt", RoleOperator, time.Hour)

	handler := AuthMiddleware(tokenSvc, apiKeys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := GetTenantID(r.Context())
		if tenantID != "tenant-jwt" {
			t.Errorf("expected tenant-jwt, got %s", tenantID)
		}
		role, _ := r.Context().Value(RoleKey).(string)
		if role != RoleOperator {
			t.Errorf("expected operator role, got %s", role)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_NoAuth(t *testing.T) {
	tokenSvc := NewTokenService("test-secret", "test-issuer")
	apiKeys := NewAPIKeyStore()

	handler := AuthMiddleware(tokenSvc, apiKeys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidAPIKey(t *testing.T) {
	tokenSvc := NewTokenService("test-secret", "test-issuer")
	apiKeys := NewAPIKeyStore()

	handler := AuthMiddleware(tokenSvc, apiKeys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid API key")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "bad-key")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidBearerToken(t *testing.T) {
	tokenSvc := NewTokenService("test-secret", "test-issuer")
	apiKeys := NewAPIKeyStore()

	handler := AuthMiddleware(tokenSvc, apiKeys)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid bearer token")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestRequireRole_Allowed(t *testing.T) {
	handler := RequireRole(RoleAdmin, RoleOperator)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx := context.WithValue(context.Background(), RoleKey, RoleAdmin)
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	handler := RequireRole(RoleAdmin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for forbidden role")
	}))

	ctx := context.WithValue(context.Background(), RoleKey, RoleViewer)
	req := httptest.NewRequest("GET", "/test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestGetTenantID(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantIDKey, "my-tenant")
	id := GetTenantID(ctx)
	if id != "my-tenant" {
		t.Errorf("expected my-tenant, got %s", id)
	}
}

func TestGetTenantID_Empty(t *testing.T) {
	id := GetTenantID(context.Background())
	if id != "" {
		t.Errorf("expected empty string, got %s", id)
	}
}
