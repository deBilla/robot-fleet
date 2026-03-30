package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
)

// BillingHandler implements thin HTTP adapters for billing endpoints.
type BillingHandler struct {
	svc *service.TemporalBillingService
}

// NewBillingHandler creates a new billing handler.
func NewBillingHandler(svc *service.TemporalBillingService) *BillingHandler {
	return &BillingHandler{svc: svc}
}

// GetSubscription returns the current subscription for the authenticated tenant.
func (h *BillingHandler) GetSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	tenant, err := h.svc.GetSubscription(r.Context(), tenantID)
	if err != nil {
		slog.Error("get subscription failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusNotFound, "subscription not found")
		return
	}

	writeJSON(w, http.StatusOK, tenant)
}

// ChangeTier handles tier change requests by signaling the billing workflow.
func (h *BillingHandler) ChangeTier(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Tier == "" {
		writeError(w, http.StatusBadRequest, "tier is required")
		return
	}

	if err := h.svc.ChangeTier(r.Context(), tenantID, req.Tier); err != nil {
		slog.Error("change tier failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to change tier")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "tier change initiated", "new_tier": req.Tier})
}

// ListInvoices returns invoices for the authenticated tenant.
func (h *BillingHandler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	invoices, err := h.svc.ListInvoices(r.Context(), tenantID, limit)
	if err != nil {
		slog.Error("list invoices failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to list invoices")
		return
	}

	writeJSON(w, http.StatusOK, invoices)
}

// GetInvoice returns a specific invoice by ID.
func (h *BillingHandler) GetInvoice(w http.ResponseWriter, r *http.Request) {
	invoiceID := r.PathValue("id")

	inv, err := h.svc.GetPersistedInvoice(r.Context(), invoiceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}

	writeJSON(w, http.StatusOK, inv)
}

// RetryPayment signals the billing workflow to retry a failed payment.
func (h *BillingHandler) RetryPayment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	if err := h.svc.RetryPayment(r.Context(), tenantID); err != nil {
		slog.Error("retry payment failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to retry payment")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "payment retry initiated"})
}

// StartBillingCycle starts a billing cycle workflow for a tenant (admin endpoint).
func (h *BillingHandler) StartBillingCycle(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Tier == "" {
		req.Tier = "free"
	}

	if err := h.svc.StartBillingCycle(r.Context(), tenantID, req.Tier); err != nil {
		slog.Error("start billing cycle failed", "error", err, "tenant", tenantID)
		writeError(w, http.StatusInternalServerError, "failed to start billing cycle")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "billing cycle started"})
}
