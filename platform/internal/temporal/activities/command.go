package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// CommandActivities holds dependencies for command-related Temporal activities.
type CommandActivities struct {
	Repo  store.RobotRepository
	Cache store.CacheStore
}

// WriteAuditInput is the input for WriteCommandAudit activity.
type WriteAuditInput struct {
	CommandID      int64          `json:"command_id"`
	RobotID        string         `json:"robot_id"`
	TenantID       string         `json:"tenant_id"`
	CommandType    string         `json:"command_type"`
	Params         map[string]any `json:"params"`
	Status         string         `json:"status"`
	Instruction    string         `json:"instruction"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// PublishCommandInput is the input for PublishCommand activity.
type PublishCommandInput struct {
	RobotID    string         `json:"robot_id"`
	CommandID  int64          `json:"command_id"`
	CmdType    string         `json:"cmd_type"`
	Params     map[string]any `json:"params"`
	WorkflowID string         `json:"workflow_id"` // forwarded in ack for bridge
}

// WriteCommandAudit persists a command audit entry to Postgres.
func (a *CommandActivities) WriteCommandAudit(ctx context.Context, input WriteAuditInput) error {
	payloadJSON, _ := json.Marshal(input.Params)
	return a.Repo.InsertCommandAudit(ctx, &store.CommandAuditEntry{
		CommandID:      fmt.Sprintf("%d", input.CommandID),
		RobotID:        input.RobotID,
		TenantID:       input.TenantID,
		CommandType:    input.CommandType,
		Payload:        payloadJSON,
		Status:         input.Status,
		Instruction:    input.Instruction,
		IdempotencyKey: input.IdempotencyKey,
	})
}

// PublishCommand publishes a command to Redis pub/sub, bridging to the gRPC stream.
func (a *CommandActivities) PublishCommand(ctx context.Context, input PublishCommandInput) error {
	cmdData, err := json.Marshal(map[string]any{
		"robot_id":    input.RobotID,
		"command":     map[string]any{"type": input.CmdType, "params": input.Params},
		"issued_at":   time.Now().UTC(),
		"command_id":  input.CommandID,
		"workflow_id": input.WorkflowID,
	})
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	return a.Cache.PublishEvent(ctx, "commands:"+input.RobotID, cmdData)
}

// UpdateCommandAuditStatus updates a command's status in the audit trail.
func (a *CommandActivities) UpdateCommandAuditStatus(ctx context.Context, commandID int64, status string) error {
	return a.Repo.UpdateCommandAuditStatus(ctx, fmt.Sprintf("%d", commandID), status)
}
