package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
)

// WebhookHandler implements HTTP endpoints for webhook management.
type WebhookHandler struct {
	svc service.WebhookService
}

// NewWebhookHandler creates a new WebhookHandler.
func NewWebhookHandler(svc service.WebhookService) *WebhookHandler {
	return &WebhookHandler{svc: svc}
}

// RegisterWebhook handles POST /api/v1/webhooks.
func (h *WebhookHandler) RegisterWebhook(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL == "" || len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "url and events are required")
		return
	}

	wh, err := h.svc.Register(r.Context(), tenantID, req.URL, req.Events, req.Secret)
	if err != nil {
		slog.Error("register webhook failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to register webhook")
		return
	}

	writeJSON(w, http.StatusCreated, wh)
}

// ListWebhooks handles GET /api/v1/webhooks.
func (h *WebhookHandler) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	webhooks, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		slog.Error("list webhooks failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list webhooks")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"webhooks": webhooks,
		"total":    len(webhooks),
	})
}

// DeleteWebhook handles DELETE /api/v1/webhooks/{id}.
func (h *WebhookHandler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	id := r.PathValue("id")

	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		slog.Error("delete webhook failed", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete webhook")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
