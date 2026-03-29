package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// Training job status constants.
const (
	TrainingStatusQueued    = "queued"
	TrainingStatusRunning   = "running"
	TrainingStatusCompleted = "completed"
	TrainingStatusFailed    = "failed"
)

// Training defaults.
const (
	DefaultTrainingTimesteps = 1_000_000
	DefaultTrainingAlgorithm = "PPO"
	DefaultTrainingEnv       = "Humanoid-v4"
	DefaultTrainingDevice    = "auto"
	DefaultEvalScenarios     = 100
)

// SubmitTrainingJobRequest holds the input for submitting a training job.
type SubmitTrainingJobRequest struct {
	AgentID     string         `json:"agent_id,omitempty"`
	Algorithm   string         `json:"algorithm"`
	Environment string         `json:"environment"`
	Timesteps   int64          `json:"timesteps"`
	Device      string         `json:"device"`
	Config      map[string]any `json:"config"`
}

// SubmitEvalRequest holds the input for submitting a policy evaluation.
type SubmitEvalRequest struct {
	JobID     string `json:"job_id"`
	Scenarios int    `json:"scenarios"`
}

// TrainingCallbackPayload is the payload sent by the training container on completion.
type TrainingCallbackPayload struct {
	JobID          string         `json:"job_id"`
	Status         string         `json:"status"`
	ModelPath      string         `json:"model_path"`
	ArtifactURL    string         `json:"artifact_url,omitempty"`
	Metrics        map[string]any `json:"metrics,omitempty"`
	ErrorMessage   string         `json:"error_message,omitempty"`
	EvalMeanReward float64        `json:"eval_mean_reward,omitempty"`
}

// EvalCallbackPayload is the payload sent by the eval container on completion.
type EvalCallbackPayload struct {
	EvalID          string         `json:"eval_id"`
	Status          string         `json:"status"`
	ScenariosTotal  int            `json:"scenarios_total"`
	ScenariosPassed int            `json:"scenarios_passed"`
	PassRate        float64        `json:"pass_rate"`
	Results         map[string]any `json:"results,omitempty"`
	ErrorMessage    string         `json:"error_message,omitempty"`
}

// JobSubmitter abstracts Kubernetes Job creation so we can mock it in tests.
type JobSubmitter interface {
	SubmitTrainingJob(ctx context.Context, job *store.TrainingJobRecord) error
	SubmitEvalJob(ctx context.Context, eval *store.TrainingEvalRecord, artifactURL string) error
}

// TrainingService defines the business logic for Cyclotron-style training.
type TrainingService interface {
	SubmitJob(ctx context.Context, tenantID, initiatedBy string, req SubmitTrainingJobRequest) (*store.TrainingJobRecord, error)
	GetJob(ctx context.Context, tenantID, jobID string) (*store.TrainingJobRecord, error)
	ListJobs(ctx context.Context, tenantID, status string, limit int) ([]*store.TrainingJobRecord, error)
	HandleTrainingCallback(ctx context.Context, payload TrainingCallbackPayload) error
	SubmitEval(ctx context.Context, tenantID string, req SubmitEvalRequest) (*store.TrainingEvalRecord, error)
	GetEval(ctx context.Context, tenantID, evalID string) (*store.TrainingEvalRecord, error)
	ListEvals(ctx context.Context, tenantID, jobID string, limit int) ([]*store.TrainingEvalRecord, error)
	HandleEvalCallback(ctx context.Context, payload EvalCallbackPayload) error
}

type trainingService struct {
	repo      store.TrainingRepository
	submitter JobSubmitter
}

// NewTrainingService creates a new training service.
func NewTrainingService(repo store.TrainingRepository, submitter JobSubmitter) TrainingService {
	return &trainingService{repo: repo, submitter: submitter}
}

func (s *trainingService) SubmitJob(ctx context.Context, tenantID, initiatedBy string, req SubmitTrainingJobRequest) (*store.TrainingJobRecord, error) {
	algorithm := req.Algorithm
	if algorithm == "" {
		algorithm = DefaultTrainingAlgorithm
	}
	env := req.Environment
	if env == "" {
		env = DefaultTrainingEnv
	}
	timesteps := req.Timesteps
	if timesteps <= 0 {
		timesteps = DefaultTrainingTimesteps
	}
	device := req.Device
	if device == "" {
		device = DefaultTrainingDevice
	}
	config := req.Config
	if config == nil {
		config = map[string]any{}
	}

	job := &store.TrainingJobRecord{
		ID:          generateUUID(),
		TenantID:    tenantID,
		AgentID:     req.AgentID,
		Status:      TrainingStatusQueued,
		Algorithm:   algorithm,
		Environment: env,
		Timesteps:   timesteps,
		Device:      device,
		Config:      config,
		Metrics:     map[string]any{},
		InitiatedBy: initiatedBy,
		CreatedAt:   time.Now().UTC(),
	}

	if err := s.repo.CreateTrainingJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create training job: %w", err)
	}

	// Submit to Kubernetes/Kubeflow (async — errors are non-fatal, job stays queued)
	if s.submitter != nil {
		if err := s.submitter.SubmitTrainingJob(ctx, job); err != nil {
			// Mark as failed if submission itself fails
			_ = s.repo.UpdateTrainingJobCompleted(ctx, job.ID, TrainingStatusFailed, "", err.Error())
			job.Status = TrainingStatusFailed
			job.ErrorMessage = err.Error()
			return job, nil
		}
		_ = s.repo.UpdateTrainingJobStatus(ctx, job.ID, TrainingStatusRunning)
		job.Status = TrainingStatusRunning
	}

	return job, nil
}

func (s *trainingService) GetJob(ctx context.Context, tenantID, jobID string) (*store.TrainingJobRecord, error) {
	job, err := s.repo.GetTrainingJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return job, nil
}

func (s *trainingService) ListJobs(ctx context.Context, tenantID, status string, limit int) ([]*store.TrainingJobRecord, error) {
	return s.repo.ListTrainingJobs(ctx, tenantID, status, limit)
}

func (s *trainingService) HandleTrainingCallback(ctx context.Context, payload TrainingCallbackPayload) error {
	status := TrainingStatusCompleted
	if payload.Status == "failed" || payload.ErrorMessage != "" {
		status = TrainingStatusFailed
	}

	if err := s.repo.UpdateTrainingJobCompleted(ctx, payload.JobID, status, payload.ModelPath, payload.ErrorMessage); err != nil {
		return fmt.Errorf("update training job on callback: %w", err)
	}

	if payload.Metrics != nil {
		_ = s.repo.UpdateTrainingJobMetrics(ctx, payload.JobID, payload.Metrics)
	}

	return nil
}

func (s *trainingService) SubmitEval(ctx context.Context, tenantID string, req SubmitEvalRequest) (*store.TrainingEvalRecord, error) {
	// Verify the training job exists and belongs to the tenant
	job, err := s.GetJob(ctx, tenantID, req.JobID)
	if err != nil {
		return nil, fmt.Errorf("get training job for eval: %w", err)
	}

	if job.Status != TrainingStatusCompleted {
		return nil, fmt.Errorf("training job %s is %s, must be completed before evaluation", req.JobID, job.Status)
	}

	scenarios := req.Scenarios
	if scenarios <= 0 {
		scenarios = DefaultEvalScenarios
	}

	eval := &store.TrainingEvalRecord{
		ID:             generateUUID(),
		TenantID:       tenantID,
		JobID:          req.JobID,
		Status:         TrainingStatusQueued,
		ScenariosTotal: scenarios,
		Metrics:        map[string]any{},
		Results:        map[string]any{},
		CreatedAt:      time.Now().UTC(),
	}

	if err := s.repo.CreateTrainingEval(ctx, eval); err != nil {
		return nil, fmt.Errorf("create training eval: %w", err)
	}

	if s.submitter != nil {
		if err := s.submitter.SubmitEvalJob(ctx, eval, job.ArtifactURL); err != nil {
			_ = s.repo.UpdateTrainingEvalCompleted(ctx, eval.ID, 0, scenarios, 0, nil, err.Error())
			eval.Status = TrainingStatusFailed
			eval.ErrorMessage = err.Error()
			return eval, nil
		}
	}

	return eval, nil
}

func (s *trainingService) GetEval(ctx context.Context, tenantID, evalID string) (*store.TrainingEvalRecord, error) {
	eval, err := s.repo.GetTrainingEval(ctx, evalID)
	if err != nil {
		return nil, err
	}
	if eval.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return eval, nil
}

func (s *trainingService) ListEvals(ctx context.Context, tenantID, jobID string, limit int) ([]*store.TrainingEvalRecord, error) {
	return s.repo.ListTrainingEvals(ctx, tenantID, jobID, limit)
}

func (s *trainingService) HandleEvalCallback(ctx context.Context, payload EvalCallbackPayload) error {
	errorMsg := payload.ErrorMessage
	return s.repo.UpdateTrainingEvalCompleted(
		ctx, payload.EvalID, payload.ScenariosPassed, payload.ScenariosTotal,
		payload.PassRate, payload.Results, errorMsg,
	)
}
