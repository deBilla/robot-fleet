package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
)

const (
	DefaultListLimit  = 20
	MaxListLimit      = 100
)

// Handler implements thin HTTP adapters that delegate to the service layer.
type Handler struct {
	svc     service.RobotService
	cache   store.CacheStore   // for WebSocket subscription only
	apiKeys *auth.APIKeyStore  // for WebSocket auth only
}

// NewHandler creates a new Handler with the given service and supporting dependencies.
func NewHandler(svc service.RobotService, cache store.CacheStore, apiKeys *auth.APIKeyStore) *Handler {
	return &Handler{svc: svc, cache: cache, apiKeys: apiKeys}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ListRobots returns all robots for the authenticated tenant.
func (h *Handler) ListRobots(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	result, err := h.svc.ListRobots(r.Context(), tenantID, limit, offset)
	if err != nil {
		slog.Error("list robots failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list robots")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetRobot returns a single robot by ID.
func (h *Handler) GetRobot(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")

	result, err := h.svc.GetRobot(r.Context(), robotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "robot not found")
		return
	}

	// Return whichever source had the data, preserving API contract
	if result.HotState != nil {
		writeJSON(w, http.StatusOK, result.HotState)
	} else {
		writeJSON(w, http.StatusOK, result.Record)
	}
}

// SendCommand sends a command to a robot.
func (h *Handler) SendCommand(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")

	var cmd struct {
		Type   string         `json:"type"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.GetTenantID(r.Context())
	result, err := h.svc.SendCommand(r.Context(), robotID, cmd.Type, cmd.Params, tenantID)
	if err != nil {
		slog.Error("send command failed", "robot", robotID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send command")
		return
	}

	writeJSON(w, http.StatusAccepted, result)
}

// GetTelemetry returns recent telemetry for a robot.
func (h *Handler) GetTelemetry(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")

	result, err := h.svc.GetTelemetry(r.Context(), robotID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no telemetry available")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// RunInference forwards an inference request to the AI service.
func (h *Handler) RunInference(w http.ResponseWriter, r *http.Request) {
	var req service.InferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.GetTenantID(r.Context())
	body, err := h.svc.RunInference(r.Context(), req, tenantID)
	if err != nil {
		slog.Error("inference failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "inference service unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// SemanticCommand interprets a natural language instruction and converts it to robot actions.
func (h *Handler) SemanticCommand(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")

	var req struct {
		Instruction string `json:"instruction"`
		RobotID     string `json:"robot_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tenantID := auth.GetTenantID(r.Context())
	result, err := h.svc.SemanticCommand(r.Context(), robotID, req.Instruction, tenantID)
	if err != nil {
		slog.Error("semantic command failed", "robot", robotID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to process command")
		return
	}

	writeJSON(w, http.StatusAccepted, result)
}

// GetFleetMetrics returns aggregated fleet metrics.
func (h *Handler) GetFleetMetrics(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	result, err := h.svc.GetFleetMetrics(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get metrics")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetCommandHistory returns the command audit trail for a specific robot.
func (h *Handler) GetCommandHistory(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")
	tenantID := auth.GetTenantID(r.Context())

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	result, err := h.svc.GetCommandHistory(r.Context(), robotID, tenantID, limit)
	if err != nil {
		slog.Error("get command history failed", "robot", robotID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get command history")
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetUsage returns API usage stats for the authenticated tenant.
func (h *Handler) GetUsage(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	result, err := h.svc.GetUsage(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get usage")
		return
	}

	writeJSON(w, http.StatusOK, result)
}
