package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// stubAdminService implements service.AdminService for handler tests.
type stubAdminService struct {
	tenants map[string]*store.TenantRecord
	keys    map[string][]*store.APIKeyRecord
}

func newStubAdminService() *stubAdminService {
	return &stubAdminService{
		tenants: map[string]*store.TenantRecord{
			"tenant-1": {ID: "tenant-1", Name: "Test Corp", Tier: "pro"},
		},
		keys: map[string][]*store.APIKeyRecord{
			"tenant-1": {
				{KeyHash: "abc123", TenantID: "tenant-1", Name: "key1", Role: "admin"},
			},
		},
	}
}

func (s *stubAdminService) CreateTenant(_ context.Context, req service.CreateTenantRequest) (*service.CreateTenantResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name required")
	}
	t := &store.TenantRecord{ID: "tenant-new", Name: req.Name, Tier: "free"}
	return &service.CreateTenantResult{Tenant: t, APIKey: "fos_testapikey123"}, nil
}

func (s *stubAdminService) GetTenant(_ context.Context, id string) (*store.TenantRecord, error) {
	t, ok := s.tenants[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return t, nil
}

func (s *stubAdminService) ListTenants(_ context.Context) ([]*store.TenantRecord, error) {
	var out []*store.TenantRecord
	for _, t := range s.tenants {
		out = append(out, t)
	}
	return out, nil
}

func (s *stubAdminService) UpdateTenant(_ context.Context, id string, _ service.UpdateTenantRequest) error {
	if _, ok := s.tenants[id]; !ok {
		return fmt.Errorf("not found")
	}
	return nil
}

func (s *stubAdminService) CreateAPIKey(_ context.Context, tenantID string, req service.CreateAPIKeyRequest) (*service.CreateAPIKeyResult, error) {
	return &service.CreateAPIKeyResult{
		PlaintextKey: "fos_newkey456",
		Record:       &store.APIKeyRecord{KeyHash: "newhash", TenantID: tenantID, Name: req.Name, Role: "developer"},
	}, nil
}

func (s *stubAdminService) ListAPIKeys(_ context.Context, tenantID string) ([]*store.APIKeyRecord, error) {
	return s.keys[tenantID], nil
}

func (s *stubAdminService) RevokeAPIKey(_ context.Context, _ string) error {
	return nil
}

func adminRequest(method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	ctx := context.WithValue(r.Context(), auth.TenantIDKey, "tenant-dev")
	ctx = context.WithValue(ctx, auth.RoleKey, auth.RoleAdmin)
	return r.WithContext(ctx)
}

func TestAdminHandler_CreateTenant(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("POST", "/api/v1/admin/tenants", `{"name":"New Corp","tier":"free"}`)
	w := httptest.NewRecorder()

	handler.CreateTenant(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["api_key"] == nil || resp["api_key"] == "" {
		t.Error("expected api_key in response")
	}
	if resp["warning"] == nil {
		t.Error("expected warning about saving key")
	}
}

func TestAdminHandler_CreateTenant_MissingName(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("POST", "/api/v1/admin/tenants", `{"tier":"free"}`)
	w := httptest.NewRecorder()

	handler.CreateTenant(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAdminHandler_ListTenants(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("GET", "/api/v1/admin/tenants", "")
	w := httptest.NewRecorder()

	handler.ListTenants(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminHandler_GetTenant(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("GET", "/api/v1/admin/tenants/tenant-1", "")
	req.SetPathValue("id", "tenant-1")
	w := httptest.NewRecorder()

	handler.GetTenant(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminHandler_GetTenant_NotFound(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("GET", "/api/v1/admin/tenants/nonexistent", "")
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()

	handler.GetTenant(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminHandler_CreateAPIKey(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("POST", "/api/v1/admin/tenants/tenant-1/keys", `{"name":"My Key","role":"developer"}`)
	req.SetPathValue("id", "tenant-1")
	w := httptest.NewRecorder()

	handler.CreateAPIKey(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["api_key"] == nil {
		t.Error("expected api_key in response")
	}
	if resp["key_hash"] == nil {
		t.Error("expected key_hash in response")
	}
}

func TestAdminHandler_ListAPIKeys(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("GET", "/api/v1/admin/tenants/tenant-1/keys", "")
	req.SetPathValue("id", "tenant-1")
	w := httptest.NewRecorder()

	handler.ListAPIKeys(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminHandler_RevokeAPIKey(t *testing.T) {
	handler := NewAdminHandler(newStubAdminService())
	req := adminRequest("DELETE", "/api/v1/admin/keys/abc123", "")
	req.SetPathValue("hash", "abc123")
	w := httptest.NewRecorder()

	handler.RevokeAPIKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireRole_BlocksNonAdmin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := auth.RequireRole(auth.RoleAdmin)(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), auth.RoleKey, auth.RoleViewer)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	protected.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d", w.Code)
	}
}

func TestRequireRole_AllowsAdmin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	protected := auth.RequireRole(auth.RoleAdmin)(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), auth.RoleKey, auth.RoleAdmin)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	protected.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin role, got %d", w.Code)
	}
}
