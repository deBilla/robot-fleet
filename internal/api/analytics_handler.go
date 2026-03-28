package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/dimuthu/robot-fleet/internal/service"
)

// AnalyticsHandler implements HTTP adapters for the analytics service.
type AnalyticsHandler struct {
	svc service.AnalyticsService
}

// NewAnalyticsHandler creates a new AnalyticsHandler.
func NewAnalyticsHandler(svc service.AnalyticsService) *AnalyticsHandler {
	return &AnalyticsHandler{svc: svc}
}

func parseTimeRange(r *http.Request) (time.Time, time.Time) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		from = time.Now().Truncate(24 * time.Hour)
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		to = time.Now()
	}
	// Extend 'to' to end of day
	to = to.Add(24*time.Hour - time.Nanosecond)
	return from, to
}

// GetFleetAnalytics returns fleet-wide hourly metrics from ClickHouse (cached in Redis).
func (h *AnalyticsHandler) GetFleetAnalytics(w http.ResponseWriter, r *http.Request) {
	from, to := parseTimeRange(r)

	metrics, err := h.svc.GetFleetAnalytics(r.Context(), from, to)
	if err != nil {
		slog.Error("fleet analytics failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get fleet analytics")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": metrics,
		"from":    from,
		"to":      to,
		"total":   len(metrics),
	})
}

// GetRobotAnalytics returns per-robot hourly metrics.
func (h *AnalyticsHandler) GetRobotAnalytics(w http.ResponseWriter, r *http.Request) {
	robotID := r.PathValue("id")
	from, to := parseTimeRange(r)

	metrics, err := h.svc.GetRobotAnalytics(r.Context(), robotID, from, to)
	if err != nil {
		slog.Error("robot analytics failed", "robot", robotID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get robot analytics")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"robot_id": robotID,
		"metrics":  metrics,
		"from":     from,
		"to":       to,
		"total":    len(metrics),
	})
}

// GetAnomalies returns detected anomalies.
func (h *AnalyticsHandler) GetAnomalies(w http.ResponseWriter, r *http.Request) {
	from, to := parseTimeRange(r)

	anomalies, err := h.svc.GetAnomalies(r.Context(), from, to)
	if err != nil {
		slog.Error("anomalies query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get anomalies")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"anomalies": anomalies,
		"from":      from,
		"to":        to,
		"total":     len(anomalies),
	})
}
