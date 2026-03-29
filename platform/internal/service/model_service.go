package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// Model lifecycle status constants.
const (
	ModelStatusStaged   = "staged"
	ModelStatusCanary   = "canary"
	ModelStatusDeployed = "deployed"
	ModelStatusArchived = "archived"
)

// ModelRegistryService defines the interface for model lifecycle management.
type ModelRegistryService interface {
	RegisterModel(ctx context.Context, req RegisterModelRequest) (*store.ModelRecord, error)
	GetModel(ctx context.Context, id string) (*store.ModelRecord, error)
	ListModels(ctx context.Context, status string) ([]*store.ModelRecord, error)
	DeployModel(ctx context.Context, id string) error
	ArchiveModel(ctx context.Context, id string) error
}

// RegisterModelRequest holds the input for registering a new model.
type RegisterModelRequest struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	ArtifactURL string         `json:"artifact_url"`
	Metrics     map[string]any `json:"metrics"`
}

type modelRegistryService struct {
	repo store.ModelRepository
}

// NewModelRegistryService creates a new model registry service.
func NewModelRegistryService(repo store.ModelRepository) ModelRegistryService {
	return &modelRegistryService{repo: repo}
}

func (s *modelRegistryService) RegisterModel(ctx context.Context, req RegisterModelRequest) (*store.ModelRecord, error) {
	id := fmt.Sprintf("%s-%s", req.Name, req.Version)
	model := &store.ModelRecord{
		ID:          id,
		Name:        req.Name,
		Version:     req.Version,
		ArtifactURL: req.ArtifactURL,
		Status:      ModelStatusStaged,
		Metrics:     req.Metrics,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.repo.RegisterModel(ctx, model); err != nil {
		return nil, fmt.Errorf("register model: %w", err)
	}
	return model, nil
}

func (s *modelRegistryService) GetModel(ctx context.Context, id string) (*store.ModelRecord, error) {
	return s.repo.GetModel(ctx, id)
}

func (s *modelRegistryService) ListModels(ctx context.Context, status string) ([]*store.ModelRecord, error) {
	return s.repo.ListModels(ctx, status)
}

func (s *modelRegistryService) DeployModel(ctx context.Context, id string) error {
	return s.repo.UpdateModelStatus(ctx, id, ModelStatusDeployed)
}

func (s *modelRegistryService) ArchiveModel(ctx context.Context, id string) error {
	return s.repo.UpdateModelStatus(ctx, id, ModelStatusArchived)
}
