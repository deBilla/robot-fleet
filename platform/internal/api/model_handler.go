package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dimuthu/robot-fleet/internal/service"
)

// ModelHandler implements thin HTTP adapters for the model registry.
type ModelHandler struct {
	svc service.ModelRegistryService
}

// NewModelHandler creates a new ModelHandler.
func NewModelHandler(svc service.ModelRegistryService) *ModelHandler {
	return &ModelHandler{svc: svc}
}

// RegisterModel creates a new model in the registry.
func (h *ModelHandler) RegisterModel(w http.ResponseWriter, r *http.Request) {
	var req service.RegisterModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Version == "" || req.ArtifactURL == "" {
		writeError(w, http.StatusBadRequest, "name, version, and artifact_url are required")
		return
	}

	model, err := h.svc.RegisterModel(r.Context(), req)
	if err != nil {
		slog.Error("register model failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to register model")
		return
	}

	writeJSON(w, http.StatusCreated, model)
}

// GetModel returns a model by ID.
func (h *ModelHandler) GetModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	model, err := h.svc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "model not found")
		return
	}

	writeJSON(w, http.StatusOK, model)
}

// ListModels returns all models, optionally filtered by status.
func (h *ModelHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")

	models, err := h.svc.ListModels(r.Context(), status)
	if err != nil {
		slog.Error("list models failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list models")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"models": models,
		"total":  len(models),
	})
}

// DeployModel transitions a model to deployed status.
func (h *ModelHandler) DeployModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.svc.DeployModel(r.Context(), id); err != nil {
		slog.Error("deploy model failed", "model", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to deploy model")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deployed", "model_id": id})
}

// ArchiveModel transitions a model to archived status.
func (h *ModelHandler) ArchiveModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.svc.ArchiveModel(r.Context(), id); err != nil {
		slog.Error("archive model failed", "model", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to archive model")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "archived", "model_id": id})
}
