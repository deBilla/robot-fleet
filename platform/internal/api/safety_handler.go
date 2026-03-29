package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dimuthu/robot-fleet/internal/service"
)

// SafetyHandler implements HTTP endpoints for safety incident tracking.
type SafetyHandler struct {
	svc service.SafetyService
}

// NewSafetyHandler creates a new SafetyHandler.
func NewSafetyHandler(svc service.SafetyService) *SafetyHandler {
	return &SafetyHandler{svc: svc}
}

// ListIncidents handles GET /api/v1/safety/incidents.
func (h *SafetyHandler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	severity := r.URL.Query().Get("severity")
	robotID := r.URL.Query().Get("robot_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	incidents, err := h.svc.ListIncidents(r.Context(), severity, robotID, limit)
	if err != nil {
		slog.Error("list safety incidents failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"incidents": incidents,
		"total":     len(incidents),
	})
}

// ReportIncident handles POST /api/v1/safety/incidents.
func (h *SafetyHandler) ReportIncident(w http.ResponseWriter, r *http.Request) {
	var req service.ReportIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	incident, err := h.svc.ReportIncident(r.Context(), req)
	if err != nil {
		slog.Error("report incident failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to report incident")
		return
	}

	writeJSON(w, http.StatusCreated, incident)
}
