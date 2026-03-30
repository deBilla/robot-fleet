package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// AdminHandler implements thin HTTP adapters for admin endpoints.
type AdminHandler struct {
	svc service.AdminService
}

// NewAdminHandler creates a new admin handler.
func NewAdminHandler(svc service.AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

// CreateTenant creates a new tenant with an initial API key.
func (h *AdminHandler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req service.CreateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	result, err := h.svc.CreateTenant(r.Context(), req)
	if err != nil {
		slog.Error("create tenant failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create tenant")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"tenant":  result.Tenant,
		"api_key": result.APIKey,
		"warning": "Save this API key now. It cannot be retrieved again.",
	})
}

// ListTenants returns all tenants.
func (h *AdminHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.svc.ListTenants(r.Context())
	if err != nil {
		slog.Error("list tenants failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list tenants")
		return
	}
	if tenants == nil {
		tenants = []*store.TenantRecord{}
	}
	writeJSON(w, http.StatusOK, tenants)
}

// GetTenant returns a single tenant.
func (h *AdminHandler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")

	tenant, err := h.svc.GetTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	writeJSON(w, http.StatusOK, tenant)
}

// UpdateTenant updates a tenant's name and billing email.
func (h *AdminHandler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")

	var req service.UpdateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.UpdateTenant(r.Context(), tenantID, req); err != nil {
		slog.Error("update tenant failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to update tenant")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// CreateAPIKey generates a new API key for a tenant.
func (h *AdminHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")

	var req service.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.svc.CreateAPIKey(r.Context(), tenantID, req)
	if err != nil {
		slog.Error("create api key failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"api_key":  result.PlaintextKey,
		"key_hash": result.Record.KeyHash,
		"name":     result.Record.Name,
		"role":     result.Record.Role,
		"warning":  "Save this API key now. It cannot be retrieved again.",
	})
}

// ListAPIKeys returns all API keys for a tenant.
func (h *AdminHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("id")

	keys, err := h.svc.ListAPIKeys(r.Context(), tenantID)
	if err != nil {
		slog.Error("list api keys failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to list api keys")
		return
	}
	if keys == nil {
		keys = []*store.APIKeyRecord{}
	}
	writeJSON(w, http.StatusOK, keys)
}

// RevokeAPIKey revokes an API key by its hash.
func (h *AdminHandler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	keyHash := r.PathValue("hash")

	if err := h.svc.RevokeAPIKey(r.Context(), keyHash); err != nil {
		slog.Error("revoke api key failed", "error", err, "hash", keyHash)
		writeError(w, http.StatusInternalServerError, "failed to revoke api key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
