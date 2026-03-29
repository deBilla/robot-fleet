package store

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RobotRepository defines the contract for persistent robot storage.
type RobotRepository interface {
	UpsertRobot(ctx context.Context, r *RobotRecord) error
	GetRobot(ctx context.Context, id string) (*RobotRecord, error)
	ListRobots(ctx context.Context, tenantID string, limit, offset int) ([]*RobotRecord, int, error)
	StoreTelemetryEvent(ctx context.Context, robotID, eventType string, payload []byte, ts time.Time) error
	StoreAPIUsage(ctx context.Context, tenantID, endpoint, method string, statusCode int, latencyMs int64) error
	// Command audit trail (NFR7)
	InsertCommandAudit(ctx context.Context, entry *CommandAuditEntry) error
	ListCommandAudit(ctx context.Context, robotID, tenantID string, limit int) ([]*CommandAuditEntry, error)
	UpdateCommandAuditStatus(ctx context.Context, commandID, status string) error
	Close()
}

// CacheStore defines the contract for hot state caching and real-time operations.
type CacheStore interface {
	SetRobotState(ctx context.Context, state *RobotHotState) error
	GetRobotState(ctx context.Context, robotID string) (*RobotHotState, error)
	CheckRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time, error)
	IncrementUsageCounter(ctx context.Context, tenantID, metric string) (int64, error)
	GetUsageCounter(ctx context.Context, tenantID, metric, date string) (int64, error)
	PublishEvent(ctx context.Context, channel string, data []byte) error
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
	// Generic cache operations for arbitrary JSON data
	SetCacheJSON(ctx context.Context, key string, data []byte, ttl time.Duration) error
	GetCacheJSON(ctx context.Context, key string) ([]byte, error)
	// Command idempotency dedup cache
	CheckCommandDedup(ctx context.Context, dedupKey string) (int64, error)
	SetCommandDedup(ctx context.Context, dedupKey string, commandID int64) error
	Close()
}

// ModelRepository defines the contract for model registry operations.
type ModelRepository interface {
	RegisterModel(ctx context.Context, m *ModelRecord) error
	GetModel(ctx context.Context, id string) (*ModelRecord, error)
	ListModels(ctx context.Context, status string) ([]*ModelRecord, error)
	UpdateModelStatus(ctx context.Context, id, status string) error
}

// WebhookRecord represents a registered webhook.
type WebhookRecord struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Secret    string    `json:"-"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookRepository defines the contract for webhook storage.
type WebhookRepository interface {
	CreateWebhook(ctx context.Context, w *WebhookRecord) error
	ListWebhooks(ctx context.Context, tenantID string) ([]*WebhookRecord, error)
	ListWebhooksByEvent(ctx context.Context, eventType string) ([]*WebhookRecord, error)
	DeleteWebhook(ctx context.Context, tenantID, id string) error
}

// AgentRecord represents a registered agent payload.
type AgentRecord struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	Runtime         string         `json:"runtime"`
	Entrypoint      string         `json:"entrypoint"`
	ArtifactURL     string         `json:"artifact_url,omitempty"`
	SafetyEnvelope  map[string]any `json:"safety_envelope"`
	MotorSkills     []string       `json:"motor_skills"`
	ModelDeps       []string       `json:"model_deps"`
	Status          string         `json:"status"`
	CreatedBy       string         `json:"created_by"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// DeploymentRecord represents an agent deployment.
type DeploymentRecord struct {
	ID                    string         `json:"id"`
	AgentID               string         `json:"agent_id"`
	TenantID              string         `json:"tenant_id"`
	Status                string         `json:"status"`
	Strategy              string         `json:"strategy"`
	CanaryPercentage      int            `json:"canary_percentage"`
	TargetFleet           []string       `json:"target_fleet"`
	SafetyEnvelopeOverride map[string]any `json:"safety_envelope_override,omitempty"`
	ValidationReport      map[string]any `json:"validation_report,omitempty"`
	RollbackReason        string         `json:"rollback_reason,omitempty"`
	InitiatedBy           string         `json:"initiated_by"`
	InitiatedAt           time.Time      `json:"initiated_at"`
	CompletedAt           *time.Time     `json:"completed_at,omitempty"`
}

// DeploymentEventRecord represents a deployment state transition event.
type DeploymentEventRecord struct {
	ID           string         `json:"id"`
	DeploymentID string         `json:"deployment_id"`
	EventType    string         `json:"event_type"`
	EventData    map[string]any `json:"event_data"`
	CreatedAt    time.Time      `json:"created_at"`
}

// AgentRepository defines the contract for agent storage.
type AgentRepository interface {
	CreateAgent(ctx context.Context, a *AgentRecord) error
	GetAgent(ctx context.Context, id string) (*AgentRecord, error)
	GetAgentByNameVersion(ctx context.Context, tenantID, name, version string) (*AgentRecord, error)
	ListAgents(ctx context.Context, tenantID, status string, limit, offset int) ([]*AgentRecord, int, error)
	UpdateAgentStatus(ctx context.Context, id, status string) error
	UpdateAgentArtifact(ctx context.Context, id, artifactURL string) error
}

// DeploymentRepository defines the contract for deployment storage.
type DeploymentRepository interface {
	CreateDeployment(ctx context.Context, d *DeploymentRecord) error
	GetDeployment(ctx context.Context, id string) (*DeploymentRecord, error)
	ListDeployments(ctx context.Context, agentID string, limit int) ([]*DeploymentRecord, error)
	UpdateDeploymentStatus(ctx context.Context, id, status string) error
	UpdateDeploymentValidation(ctx context.Context, id string, report map[string]any) error
	SetDeploymentCompleted(ctx context.Context, id string, rollbackReason string) error
	AppendDeploymentEvent(ctx context.Context, deploymentID, eventType string, data map[string]any) error
	ListDeploymentEvents(ctx context.Context, deploymentID string) ([]*DeploymentEventRecord, error)
}

// TrainingJobRecord represents a training job.
type TrainingJobRecord struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	AgentID      string         `json:"agent_id,omitempty"`
	Status       string         `json:"status"`
	Algorithm    string         `json:"algorithm"`
	Environment  string         `json:"environment"`
	Timesteps    int64          `json:"timesteps"`
	Device       string         `json:"device"`
	Config       map[string]any `json:"config"`
	Metrics      map[string]any `json:"metrics"`
	ArtifactURL  string         `json:"artifact_url,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	InitiatedBy  string         `json:"initiated_by"`
	CreatedAt    time.Time      `json:"created_at"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

// TrainingEvalRecord represents a policy evaluation run.
type TrainingEvalRecord struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	JobID           string         `json:"job_id"`
	Status          string         `json:"status"`
	ScenariosTotal  int            `json:"scenarios_total"`
	ScenariosPassed int            `json:"scenarios_passed"`
	PassRate        float64        `json:"pass_rate"`
	Metrics         map[string]any `json:"metrics"`
	Results         map[string]any `json:"results"`
	ErrorMessage    string         `json:"error_message,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

// TrainingRepository defines the contract for training job storage.
type TrainingRepository interface {
	CreateTrainingJob(ctx context.Context, j *TrainingJobRecord) error
	GetTrainingJob(ctx context.Context, id string) (*TrainingJobRecord, error)
	ListTrainingJobs(ctx context.Context, tenantID, status string, limit int) ([]*TrainingJobRecord, error)
	UpdateTrainingJobStatus(ctx context.Context, id, status string) error
	UpdateTrainingJobMetrics(ctx context.Context, id string, metrics map[string]any) error
	UpdateTrainingJobCompleted(ctx context.Context, id, status, artifactURL, errorMsg string) error
	CreateTrainingEval(ctx context.Context, e *TrainingEvalRecord) error
	GetTrainingEval(ctx context.Context, id string) (*TrainingEvalRecord, error)
	ListTrainingEvals(ctx context.Context, tenantID string, jobID string, limit int) ([]*TrainingEvalRecord, error)
	UpdateTrainingEvalCompleted(ctx context.Context, id string, passed, total int, passRate float64, results map[string]any, errorMsg string) error
}

// SafetyIncidentRecord represents a safety incident.
type SafetyIncidentRecord struct {
	ID                string         `json:"id"`
	RobotID           string         `json:"robot_id"`
	AgentID           string         `json:"agent_id,omitempty"`
	DeploymentID      string         `json:"deployment_id,omitempty"`
	SiteID            string         `json:"site_id"`
	IncidentType      string         `json:"incident_type"`
	Severity          string         `json:"severity"`
	Details           map[string]any `json:"details"`
	TelemetrySnapshot map[string]any `json:"telemetry_snapshot,omitempty"`
	ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
	Resolution        string         `json:"resolution,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

// SafetyRepository defines the contract for safety incident storage.
type SafetyRepository interface {
	CreateSafetyIncident(ctx context.Context, i *SafetyIncidentRecord) error
	ListSafetyIncidents(ctx context.Context, severity, robotID string, limit int) ([]*SafetyIncidentRecord, error)
}

// SkillRecord represents a motor skill in the catalog.
type SkillRecord struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	SkillType      string   `json:"skill_type"`
	RequiredJoints []string `json:"required_joints"`
	Version        string   `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
}

// SkillsRepository defines the contract for motor skills catalog.
type SkillsRepository interface {
	ListSkills(ctx context.Context, skillType string) ([]*SkillRecord, error)
	GetSkill(ctx context.Context, id string) (*SkillRecord, error)
}

// AuditWriter defines the contract for writing audit log entries.
type AuditWriter interface {
	WriteAuditLog(ctx context.Context, tenantID, action, resourceType, resourceID string, details map[string]any) error
}

// Compile-time interface compliance checks.
var (
	_ RobotRepository      = (*PostgresStore)(nil)
	_ ModelRepository      = (*PostgresStore)(nil)
	_ AgentRepository      = (*PostgresStore)(nil)
	_ DeploymentRepository = (*PostgresStore)(nil)
	_ TrainingRepository   = (*PostgresStore)(nil)
	_ SafetyRepository     = (*PostgresStore)(nil)
	_ SkillsRepository     = (*PostgresStore)(nil)
	_ AuditWriter          = (*PostgresStore)(nil)
	_ CacheStore           = (*RedisStore)(nil)
)
