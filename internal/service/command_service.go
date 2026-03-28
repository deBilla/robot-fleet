package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

func (s *robotService) SendCommand(ctx context.Context, robotID, cmdType string, params map[string]any) (*CommandResult, error) {
	commandID := time.Now().UnixNano()
	cmdData, err := json.Marshal(map[string]any{
		"robot_id":   robotID,
		"command":    map[string]any{"type": cmdType, "params": params},
		"issued_at":  time.Now().UTC(),
		"command_id": commandID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	if err := s.cache.PublishEvent(ctx, "commands:"+robotID, cmdData); err != nil {
		return nil, fmt.Errorf("publish command to %s: %w", robotID, err)
	}

	return &CommandResult{CommandID: commandID, Status: "queued", RobotID: robotID}, nil
}

func (s *robotService) SemanticCommand(ctx context.Context, robotID, instruction, tenantID string) (*SemanticCommandResult, error) {
	s.cache.IncrementUsageCounter(ctx, tenantID, "semantic_commands")

	resolved := s.cmdReg.Resolve(instruction)

	commandID := time.Now().UnixNano()
	cmdData, err := json.Marshal(map[string]any{
		"robot_id":             robotID,
		"command":              map[string]any{"type": resolved.Type, "params": resolved.Params},
		"issued_at":            time.Now().UTC(),
		"command_id":           commandID,
		"semantic":             true,
		"original_instruction": instruction,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal semantic command: %w", err)
	}

	if err := s.cache.PublishEvent(ctx, "commands:"+robotID, cmdData); err != nil {
		slog.Error("failed to publish semantic command", "robot", robotID, "error", err)
	}

	return &SemanticCommandResult{
		CommandID:   commandID,
		RobotID:     robotID,
		Status:      "queued",
		Interpreted: resolved,
		Original:    instruction,
	}, nil
}
