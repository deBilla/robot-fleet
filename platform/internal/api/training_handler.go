package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
)

// TrainingHandler implements Cyclotron-style HTTP endpoints for training jobs.
type TrainingHandler struct {
	svc service.TrainingService
}

// NewTrainingHandler creates a new TrainingHandler.
func NewTrainingHandler(svc service.TrainingService) *TrainingHandler {
	return &TrainingHandler{svc: svc}
}

// SubmitJob handles POST /api/v1/locomotion/jobs.
func (h *TrainingHandler) SubmitJob(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req service.SubmitTrainingJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	job, err := h.svc.SubmitJob(r.Context(), tenantID, tenantID, req)
	if err != nil {
		slog.Error("submit training job failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to submit training job")
		return
	}

	writeJSON(w, http.StatusAccepted, job)
}

// GetJob handles GET /api/v1/locomotion/jobs/{id}.
func (h *TrainingHandler) GetJob(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	jobID := r.PathValue("id")

	job, err := h.svc.GetJob(r.Context(), tenantID, jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "training job not found")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

// ListJobs handles GET /api/v1/locomotion/jobs.
func (h *TrainingHandler) ListJobs(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	jobs, err := h.svc.ListJobs(r.Context(), tenantID, status, limit)
	if err != nil {
		slog.Error("list training jobs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list training jobs")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":  jobs,
		"total": len(jobs),
	})
}

// SubmitEval handles POST /api/v1/locomotion/evals.
func (h *TrainingHandler) SubmitEval(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req service.SubmitEvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.JobID == "" {
		writeError(w, http.StatusBadRequest, "job_id is required")
		return
	}

	eval, err := h.svc.SubmitEval(r.Context(), tenantID, req)
	if err != nil {
		slog.Error("submit eval failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to submit evaluation")
		return
	}

	writeJSON(w, http.StatusAccepted, eval)
}

// GetEval handles GET /api/v1/locomotion/evals/{id}.
func (h *TrainingHandler) GetEval(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	evalID := r.PathValue("id")

	eval, err := h.svc.GetEval(r.Context(), tenantID, evalID)
	if err != nil {
		writeError(w, http.StatusNotFound, "evaluation not found")
		return
	}

	writeJSON(w, http.StatusOK, eval)
}

// ListEvals handles GET /api/v1/locomotion/evals.
func (h *TrainingHandler) ListEvals(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	jobID := r.URL.Query().Get("job_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	evals, err := h.svc.ListEvals(r.Context(), tenantID, jobID, limit)
	if err != nil {
		slog.Error("list evals failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list evaluations")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"evaluations": evals,
		"total":       len(evals),
	})
}

// TrainingCallback handles POST /api/v1/internal/training/callback (internal, from training pods).
func (h *TrainingHandler) TrainingCallback(w http.ResponseWriter, r *http.Request) {
	var payload service.TrainingCallbackPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid callback payload")
		return
	}

	if err := h.svc.HandleTrainingCallback(r.Context(), payload); err != nil {
		slog.Error("training callback failed", "job", payload.JobID, "error", err)
		writeError(w, http.StatusInternalServerError, "callback processing failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// EvalCallback handles POST /api/v1/internal/eval/callback (internal, from eval pods).
func (h *TrainingHandler) EvalCallback(w http.ResponseWriter, r *http.Request) {
	var payload service.EvalCallbackPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid callback payload")
		return
	}

	if err := h.svc.HandleEvalCallback(r.Context(), payload); err != nil {
		slog.Error("eval callback failed", "eval", payload.EvalID, "error", err)
		writeError(w, http.StatusInternalServerError, "callback processing failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
