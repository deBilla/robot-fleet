package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// --- Mock Training Repository ---

type mockTrainingRepo struct {
	jobs  map[string]*store.TrainingJobRecord
	evals map[string]*store.TrainingEvalRecord
	err   error
}

func newMockTrainingRepo() *mockTrainingRepo {
	return &mockTrainingRepo{
		jobs:  make(map[string]*store.TrainingJobRecord),
		evals: make(map[string]*store.TrainingEvalRecord),
	}
}

func (m *mockTrainingRepo) CreateTrainingJob(_ context.Context, j *store.TrainingJobRecord) error {
	if m.err != nil {
		return m.err
	}
	m.jobs[j.ID] = j
	return nil
}

func (m *mockTrainingRepo) GetTrainingJob(_ context.Context, id string) (*store.TrainingJobRecord, error) {
	j, ok := m.jobs[id]
	if !ok {
		return nil, fmt.Errorf("training job %s not found", id)
	}
	return j, nil
}

func (m *mockTrainingRepo) ListTrainingJobs(_ context.Context, tenantID, status string, limit int) ([]*store.TrainingJobRecord, error) {
	var result []*store.TrainingJobRecord
	for _, j := range m.jobs {
		if j.TenantID != tenantID {
			continue
		}
		if status != "" && j.Status != status {
			continue
		}
		result = append(result, j)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockTrainingRepo) UpdateTrainingJobStatus(_ context.Context, id, status string) error {
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	j.Status = status
	if status == "running" {
		now := time.Now()
		j.StartedAt = &now
	}
	return nil
}

func (m *mockTrainingRepo) UpdateTrainingJobMetrics(_ context.Context, id string, metrics map[string]any) error {
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	j.Metrics = metrics
	return nil
}

func (m *mockTrainingRepo) UpdateTrainingJobCompleted(_ context.Context, id, status, artifactURL, errorMsg string) error {
	j, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	j.Status = status
	j.ArtifactURL = artifactURL
	j.ErrorMessage = errorMsg
	now := time.Now()
	j.CompletedAt = &now
	return nil
}

func (m *mockTrainingRepo) CreateTrainingEval(_ context.Context, e *store.TrainingEvalRecord) error {
	if m.err != nil {
		return m.err
	}
	m.evals[e.ID] = e
	return nil
}

func (m *mockTrainingRepo) GetTrainingEval(_ context.Context, id string) (*store.TrainingEvalRecord, error) {
	e, ok := m.evals[id]
	if !ok {
		return nil, fmt.Errorf("eval %s not found", id)
	}
	return e, nil
}

func (m *mockTrainingRepo) ListTrainingEvals(_ context.Context, tenantID, jobID string, limit int) ([]*store.TrainingEvalRecord, error) {
	var result []*store.TrainingEvalRecord
	for _, e := range m.evals {
		if e.TenantID != tenantID {
			continue
		}
		if jobID != "" && e.JobID != jobID {
			continue
		}
		result = append(result, e)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockTrainingRepo) UpdateTrainingEvalCompleted(_ context.Context, id string, passed, total int, passRate float64, results map[string]any, errorMsg string) error {
	e, ok := m.evals[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	e.ScenariosPassed = passed
	e.ScenariosTotal = total
	e.PassRate = passRate
	e.Results = results
	e.ErrorMessage = errorMsg
	if errorMsg != "" {
		e.Status = "failed"
	} else {
		e.Status = "completed"
	}
	return nil
}

// --- Mock Submitter ---

type mockSubmitter struct {
	submitted []string
	err       error
}

func (m *mockSubmitter) SubmitTrainingJob(_ context.Context, job *store.TrainingJobRecord) error {
	if m.err != nil {
		return m.err
	}
	m.submitted = append(m.submitted, "train:"+job.ID)
	return nil
}

func (m *mockSubmitter) SubmitEvalJob(_ context.Context, eval *store.TrainingEvalRecord, _ string) error {
	if m.err != nil {
		return m.err
	}
	m.submitted = append(m.submitted, "eval:"+eval.ID)
	return nil
}

// --- Tests ---

func TestSubmitTrainingJob(t *testing.T) {
	repo := newMockTrainingRepo()
	submitter := &mockSubmitter{}
	svc := NewTrainingService(repo, submitter)

	job, err := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{
		Timesteps: 500_000,
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if job.ID == "" {
		t.Error("job ID should not be empty")
	}
	if job.Status != TrainingStatusRunning {
		t.Errorf("expected running (submitted to k8s), got %s", job.Status)
	}
	if job.Algorithm != "PPO" {
		t.Errorf("expected PPO default, got %s", job.Algorithm)
	}
	if job.Environment != "Humanoid-v4" {
		t.Errorf("expected Humanoid-v4 default, got %s", job.Environment)
	}
	if job.Timesteps != 500_000 {
		t.Errorf("expected 500000, got %d", job.Timesteps)
	}
	if len(submitter.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(submitter.submitted))
	}
}

func TestSubmitTrainingJob_SubmissionFailure(t *testing.T) {
	repo := newMockTrainingRepo()
	submitter := &mockSubmitter{err: fmt.Errorf("kubectl not found")}
	svc := NewTrainingService(repo, submitter)

	job, err := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})
	if err != nil {
		t.Fatalf("should not return error (failure stored on job): %v", err)
	}
	if job.Status != TrainingStatusFailed {
		t.Errorf("expected failed, got %s", job.Status)
	}
	if job.ErrorMessage == "" {
		t.Error("expected error message")
	}
}

func TestGetJob_TenantIsolation(t *testing.T) {
	repo := newMockTrainingRepo()
	svc := NewTrainingService(repo, &NoOpSubmitter{})

	job, _ := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})

	// Same tenant can access
	got, err := svc.GetJob(context.Background(), "tenant-1", job.ID)
	if err != nil {
		t.Fatalf("same tenant should access: %v", err)
	}
	if got.ID != job.ID {
		t.Error("should return same job")
	}

	// Different tenant cannot
	_, err = svc.GetJob(context.Background(), "tenant-2", job.ID)
	if err != ErrNotFound {
		t.Errorf("different tenant should get ErrNotFound, got: %v", err)
	}
}

func TestSubmitEval(t *testing.T) {
	repo := newMockTrainingRepo()
	submitter := &mockSubmitter{}
	svc := NewTrainingService(repo, submitter)

	// Create and complete a training job first
	job, _ := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})
	repo.jobs[job.ID].Status = TrainingStatusCompleted
	repo.jobs[job.ID].ArtifactURL = "training/" + job.ID + "/policy.zip"

	eval, err := svc.SubmitEval(context.Background(), "tenant-1", SubmitEvalRequest{
		JobID:     job.ID,
		Scenarios: 50,
	})
	if err != nil {
		t.Fatalf("submit eval failed: %v", err)
	}

	if eval.JobID != job.ID {
		t.Errorf("expected job ID %s, got %s", job.ID, eval.JobID)
	}
	if eval.ScenariosTotal != 50 {
		t.Errorf("expected 50 scenarios, got %d", eval.ScenariosTotal)
	}
	if len(submitter.submitted) != 2 { // 1 train + 1 eval
		t.Errorf("expected 2 submissions, got %d", len(submitter.submitted))
	}
}

func TestSubmitEval_JobNotCompleted(t *testing.T) {
	repo := newMockTrainingRepo()
	svc := NewTrainingService(repo, &mockSubmitter{})

	job, _ := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})
	// Job is still "queued" (NoOpSubmitter doesn't change status)

	_, err := svc.SubmitEval(context.Background(), "tenant-1", SubmitEvalRequest{
		JobID: job.ID,
	})
	if err == nil {
		t.Fatal("should reject eval for non-completed job")
	}
}

func TestTrainingCallback(t *testing.T) {
	repo := newMockTrainingRepo()
	svc := NewTrainingService(repo, &mockSubmitter{})

	job, _ := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})

	err := svc.HandleTrainingCallback(context.Background(), TrainingCallbackPayload{
		JobID:     job.ID,
		Status:    "completed",
		ModelPath: "training/" + job.ID + "/policy.zip",
		Metrics:   map[string]any{"mean_reward": 4500.0},
	})
	if err != nil {
		t.Fatalf("callback failed: %v", err)
	}

	updated, _ := svc.GetJob(context.Background(), "tenant-1", job.ID)
	if updated.Status != TrainingStatusCompleted {
		t.Errorf("expected completed, got %s", updated.Status)
	}
	if updated.ArtifactURL == "" {
		t.Error("artifact URL should be set")
	}
}

func TestEvalCallback(t *testing.T) {
	repo := newMockTrainingRepo()
	svc := NewTrainingService(repo, &mockSubmitter{})

	job, _ := svc.SubmitJob(context.Background(), "tenant-1", "user@test.com", SubmitTrainingJobRequest{})
	repo.jobs[job.ID].Status = TrainingStatusCompleted

	eval, _ := svc.SubmitEval(context.Background(), "tenant-1", SubmitEvalRequest{
		JobID:     job.ID,
		Scenarios: 100,
	})

	err := svc.HandleEvalCallback(context.Background(), EvalCallbackPayload{
		EvalID:          eval.ID,
		Status:          "completed",
		ScenariosTotal:  100,
		ScenariosPassed: 94,
		PassRate:        0.94,
	})
	if err != nil {
		t.Fatalf("eval callback failed: %v", err)
	}

	updated, _ := svc.GetEval(context.Background(), "tenant-1", eval.ID)
	if updated.Status != "completed" {
		t.Errorf("expected completed, got %s", updated.Status)
	}
	if updated.PassRate != 0.94 {
		t.Errorf("expected 0.94, got %f", updated.PassRate)
	}
	if updated.ScenariosPassed != 94 {
		t.Errorf("expected 94, got %d", updated.ScenariosPassed)
	}
}
