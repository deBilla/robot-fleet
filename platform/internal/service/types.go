package service

import (
	"context"
	"errors"
	"time"

	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// Sentinel errors for service-level error handling.
var (
	ErrNotFound    = errors.New("not found")
	ErrUnavailable = errors.New("service unavailable")
)

// RobotService defines the business logic interface for the FleetOS API.
type RobotService interface {
	ListRobots(ctx context.Context, tenantID string, limit, offset int) (*ListRobotsResult, error)
	GetRobot(ctx context.Context, robotID string) (*RobotResult, error)
	SendCommand(ctx context.Context, robotID, cmdType string, params map[string]any, tenantID string) (*CommandResult, error)
	GetTelemetry(ctx context.Context, robotID string) (*TelemetryResult, error)
	RunInference(ctx context.Context, req InferenceRequest, tenantID string) ([]byte, error)
	SemanticCommand(ctx context.Context, robotID, instruction, tenantID string) (*SemanticCommandResult, error)
	GetFleetMetrics(ctx context.Context, tenantID string) (*FleetMetrics, error)
	GetUsage(ctx context.Context, tenantID string) (*UsageResult, error)
	GetCommandHistory(ctx context.Context, robotID, tenantID string, limit int) ([]*store.CommandAuditEntry, error)
}

// RobotResult holds a robot from either hot state (Redis) or cold record (Postgres).
type RobotResult struct {
	HotState *store.RobotHotState `json:"hot_state,omitempty"`
	Record   *store.RobotRecord   `json:"record,omitempty"`
}

// ListRobotsResult holds paginated robot listing.
type ListRobotsResult struct {
	Robots []*store.RobotRecord `json:"robots"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
}

// CommandResult holds the outcome of a robot command.
type CommandResult struct {
	CommandID int64  `json:"command_id"`
	Status    string `json:"status"`
	RobotID   string `json:"robot_id"`
}

// TelemetryResult holds real-time telemetry for a robot.
type TelemetryResult struct {
	RobotID   string               `json:"robot_id"`
	State     *store.RobotHotState `json:"state"`
	Timestamp time.Time            `json:"timestamp"`
}

// InferenceRequest holds the input for an AI inference call.
type InferenceRequest struct {
	Image       string `json:"image"`
	Instruction string `json:"instruction"`
	ModelID     string `json:"model_id"`
	Embodiment  string `json:"embodiment"`
}

// SemanticCommandResult holds the outcome of a semantic command interpretation.
type SemanticCommandResult struct {
	CommandID   int64          `json:"command_id"`
	RobotID     string         `json:"robot_id"`
	Status      string         `json:"status"`
	Interpreted command.Result `json:"interpreted"`
	Original    string         `json:"original"`
}

// FleetMetrics holds aggregated fleet statistics.
type FleetMetrics struct {
	TotalRobots  int       `json:"total_robots"`
	ActiveRobots int       `json:"active_robots"`
	IdleRobots   int       `json:"idle_robots"`
	ErrorRobots  int       `json:"error_robots"`
	AvgBattery   float64   `json:"avg_battery"`
	Timestamp    time.Time `json:"timestamp"`
}

// UsageResult holds per-tenant API usage stats.
type UsageResult struct {
	TenantID       string `json:"tenant_id"`
	Date           string `json:"date"`
	APICalls       int64  `json:"api_calls"`
	InferenceCalls int64  `json:"inference_calls"`
}
