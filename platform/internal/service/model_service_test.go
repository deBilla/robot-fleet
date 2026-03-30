package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// mockModelRepo implements store.ModelRepository for testing.
type mockModelRepo struct {
	models map[string]*store.ModelRecord
	err    error
}

func newMockModelRepo() *mockModelRepo {
	return &mockModelRepo{models: make(map[string]*store.ModelRecord)}
}

func (m *mockModelRepo) RegisterModel(_ context.Context, rec *store.ModelRecord) error {
	if m.err != nil {
		return m.err
	}
	m.models[rec.ID] = rec
	return nil
}

func (m *mockModelRepo) GetModel(_ context.Context, id string) (*store.ModelRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	rec, ok := m.models[id]
	if !ok {
		return nil, ErrNotFound
	}
	return rec, nil
}

func (m *mockModelRepo) ListModels(_ context.Context, status string) ([]*store.ModelRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []*store.ModelRecord
	for _, r := range m.models {
		if status == "" || r.Status == status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockModelRepo) UpdateModelStatus(_ context.Context, id, status string) error {
	if m.err != nil {
		return m.err
	}
	rec, ok := m.models[id]
	if !ok {
		return ErrNotFound
	}
	rec.Status = status
	return nil
}

// --- Tests ---

func TestRegisterModel(t *testing.T) {
	repo := newMockModelRepo()
	svc := NewModelRegistryService(repo, newMockCache())

	req := RegisterModelRequest{
		Name:        "groot-n1",
		Version:     "v2.0",
		ArtifactURL: "s3://bucket/groot-n1-v2.0.pt",
		Metrics:     map[string]any{"accuracy": 0.95},
	}
	model, err := svc.RegisterModel(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if model.Status != ModelStatusStaged {
		t.Errorf("expected status=%s, got %s", ModelStatusStaged, model.Status)
	}
	if model.ID != "groot-n1-v2.0" {
		t.Errorf("expected id=groot-n1-v2.0, got %s", model.ID)
	}
}

func TestDeployModel_PublishesEvent(t *testing.T) {
	repo := newMockModelRepo()
	cache := newMockCache()
	svc := NewModelRegistryService(repo, cache)

	// Seed a staged model.
	repo.models["m1"] = &store.ModelRecord{
		ID:          "m1",
		Name:        "groot-n1",
		Version:     "v2.0",
		ArtifactURL: "s3://bucket/groot-n1-v2.0.pt",
		Status:      ModelStatusStaged,
		CreatedAt:   time.Now(),
	}

	if err := svc.DeployModel(context.Background(), "m1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Model status should be updated.
	if repo.models["m1"].Status != ModelStatusDeployed {
		t.Errorf("expected model status=deployed, got %s", repo.models["m1"].Status)
	}

	// A model:deployed event should have been published.
	if len(cache.published) != 1 || cache.published[0] != "model:deployed" {
		t.Errorf("expected published channel=model:deployed, got %v", cache.published)
	}
}

func TestDeployModel_EventPayload(t *testing.T) {
	repo := newMockModelRepo()
	cache := newMockCache()
	svc := NewModelRegistryService(repo, cache)

	repo.models["m2"] = &store.ModelRecord{
		ID:          "m2",
		Name:        "policy-v3",
		Version:     "v3.1",
		ArtifactURL: "s3://bucket/policy-v3.1.pt",
		Status:      ModelStatusStaged,
		CreatedAt:   time.Now(),
	}

	if err := svc.DeployModel(context.Background(), "m2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Capture the published payload via a custom mock that records bytes.
	// Re-run with recordingCache to capture payload bytes.
	rc := &recordingCache{}
	svc2 := NewModelRegistryService(repo, rc)
	repo.models["m2"].Status = ModelStatusStaged // reset for second run
	if err := svc2.DeployModel(context.Background(), "m2"); err != nil {
		t.Fatalf("unexpected error on second run: %v", err)
	}

	var payload map[string]string
	if err := json.Unmarshal(rc.lastPayload, &payload); err != nil {
		t.Fatalf("invalid JSON payload: %v", err)
	}
	if payload["model_id"] != "m2" {
		t.Errorf("expected model_id=m2, got %s", payload["model_id"])
	}
	if payload["version"] != "v3.1" {
		t.Errorf("expected version=v3.1, got %s", payload["version"])
	}
	if payload["artifact_url"] != "s3://bucket/policy-v3.1.pt" {
		t.Errorf("expected artifact_url=s3://bucket/policy-v3.1.pt, got %s", payload["artifact_url"])
	}
}

func TestDeployModel_NotFoundError(t *testing.T) {
	repo := newMockModelRepo()
	svc := NewModelRegistryService(repo, newMockCache())

	if err := svc.DeployModel(context.Background(), "nonexistent"); err == nil {
		t.Error("expected error for nonexistent model, got nil")
	}
}

func TestArchiveModel(t *testing.T) {
	repo := newMockModelRepo()
	svc := NewModelRegistryService(repo, newMockCache())

	repo.models["m3"] = &store.ModelRecord{ID: "m3", Status: ModelStatusDeployed}

	if err := svc.ArchiveModel(context.Background(), "m3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.models["m3"].Status != ModelStatusArchived {
		t.Errorf("expected archived, got %s", repo.models["m3"].Status)
	}
}

// recordingCache captures the last PublishEvent call payload.
type recordingCache struct {
	mockCache
	lastPayload []byte
}

func (r *recordingCache) PublishEvent(_ context.Context, _ string, data []byte) error {
	r.lastPayload = data
	return nil
}
