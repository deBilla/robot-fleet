package api

import (
	"log/slog"
	"net/http"

	"github.com/dimuthu/robot-fleet/internal/service"
)

// SkillsHandler implements HTTP endpoints for the motor skills catalog.
type SkillsHandler struct {
	svc service.SkillsService
}

// NewSkillsHandler creates a new SkillsHandler.
func NewSkillsHandler(svc service.SkillsService) *SkillsHandler {
	return &SkillsHandler{svc: svc}
}

// ListSkills handles GET /api/v1/skills.
func (h *SkillsHandler) ListSkills(w http.ResponseWriter, r *http.Request) {
	skillType := r.URL.Query().Get("type")

	skills, err := h.svc.ListSkills(r.Context(), skillType)
	if err != nil {
		slog.Error("list skills failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list skills")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"skills": skills,
		"total":  len(skills),
	})
}

// GetSkill handles GET /api/v1/skills/{id}.
func (h *SkillsHandler) GetSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	skill, err := h.svc.GetSkill(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	writeJSON(w, http.StatusOK, skill)
}
