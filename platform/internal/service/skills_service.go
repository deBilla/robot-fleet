package service

import (
	"context"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// SkillsService defines the business logic for the motor skills catalog.
type SkillsService interface {
	ListSkills(ctx context.Context, skillType string) ([]*store.SkillRecord, error)
	GetSkill(ctx context.Context, id string) (*store.SkillRecord, error)
}

type skillsService struct {
	repo store.SkillsRepository
}

// NewSkillsService creates a new skills service.
func NewSkillsService(repo store.SkillsRepository) SkillsService {
	return &skillsService{repo: repo}
}

func (s *skillsService) ListSkills(ctx context.Context, skillType string) ([]*store.SkillRecord, error) {
	return s.repo.ListSkills(ctx, skillType)
}

func (s *skillsService) GetSkill(ctx context.Context, id string) (*store.SkillRecord, error) {
	return s.repo.GetSkill(ctx, id)
}
