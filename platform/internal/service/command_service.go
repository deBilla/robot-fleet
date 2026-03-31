package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/dimuthu/robot-fleet/internal/store"
	temporalpkg "github.com/dimuthu/robot-fleet/internal/temporal"
	"github.com/dimuthu/robot-fleet/internal/temporal/workflows"
)

// commandDedupKey computes a deterministic dedup key from robot ID, command type, and params.
func commandDedupKey(robotID, cmdType string, params map[string]any) string {
	raw, _ := json.Marshal(params)
	hash := sha256.Sum256([]byte(robotID + ":" + cmdType + ":" + string(raw)))
	return fmt.Sprintf("cmd:dedup:%x", hash)
}

func (s *robotService) SendCommand(ctx context.Context, robotID, cmdType string, params map[string]any, tenantID string) (*CommandResult, error) {
	start := time.Now()
	defer func() {
		middleware.CommandDispatchDuration.Observe(time.Since(start).Seconds())
	}()

	dedupKey := commandDedupKey(robotID, cmdType, params)

	// Kafka path: publish to robot.commands topic → processor starts Temporal workflow
	if s.commandProducer != nil {
		return s.sendCommandViaKafka(ctx, robotID, cmdType, params, tenantID, dedupKey)
	}

	// Temporal direct path: start workflow without Kafka
	if s.temporalClient != nil {
		return s.sendCommandViaTemporal(ctx, robotID, cmdType, params, tenantID, dedupKey)
	}

	// Legacy path: Redis pub/sub (fire-and-forget)
	return s.sendCommandLegacy(ctx, robotID, cmdType, params, tenantID, dedupKey)
}

func (s *robotService) sendCommandViaKafka(ctx context.Context, robotID, cmdType string, params map[string]any, tenantID, dedupKey string) (*CommandResult, error) {
	commandID := time.Now().UnixNano()
	msg := CommandMessage{
		RobotID:   robotID,
		CommandID: commandID,
		CmdType:   cmdType,
		Params:    params,
		TenantID:  tenantID,
		DedupKey:  dedupKey,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal command message: %w", err)
	}
	if err := s.commandProducer.Publish(robotID, data); err != nil {
		return nil, fmt.Errorf("publish command to kafka: %w", err)
	}
	middleware.CommandsDispatched.WithLabelValues(cmdType, "queued").Inc()
	return &CommandResult{CommandID: commandID, Status: "queued", RobotID: robotID}, nil
}

func (s *robotService) sendCommandViaTemporal(ctx context.Context, robotID, cmdType string, params map[string]any, tenantID, dedupKey string) (*CommandResult, error) {
	commandID := time.Now().UnixNano()
	workflowID := fmt.Sprintf("cmd-%s-%s", robotID, dedupKey)

	we, err := s.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: temporalpkg.TaskQueueCommand,
	}, workflows.CommandDispatchWorkflow, workflows.CommandWorkflowInput{
		RobotID:  robotID,
		CommandID: commandID,
		CmdType:  cmdType,
		Params:   params,
		TenantID: tenantID,
		DedupKey: dedupKey,
	})
	if err != nil {
		// Temporal returns an error if workflow ID already exists → duplicate
		if strings.Contains(err.Error(), "already started") {
			middleware.CommandsDispatched.WithLabelValues(cmdType, "duplicate").Inc()
			return &CommandResult{CommandID: commandID, Status: "duplicate", RobotID: robotID}, nil
		}
		return nil, fmt.Errorf("start command workflow: %w", err)
	}

	middleware.CommandsDispatched.WithLabelValues(cmdType, "queued").Inc()
	slog.Info("command workflow started", "workflow_id", we.GetID(), "run_id", we.GetRunID(), "robot", robotID)

	return &CommandResult{CommandID: commandID, Status: "queued", RobotID: robotID}, nil
}

func (s *robotService) sendCommandLegacy(ctx context.Context, robotID, cmdType string, params map[string]any, tenantID, dedupKey string) (*CommandResult, error) {
	// Idempotency check: reject duplicate commands within the dedup window
	if existingID, err := s.cache.CheckCommandDedup(ctx, dedupKey); err != nil {
		slog.Error("command dedup check failed, proceeding without dedup", "error", err)
	} else if existingID != 0 {
		return &CommandResult{CommandID: existingID, Status: "duplicate", RobotID: robotID}, nil
	}

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
		middleware.CommandsDispatched.WithLabelValues(cmdType, "error").Inc()
		return nil, fmt.Errorf("publish command to %s: %w", robotID, err)
	}
	middleware.CommandsDispatched.WithLabelValues(cmdType, "queued").Inc()

	// Store dedup entry after successful publish
	if err := s.cache.SetCommandDedup(ctx, dedupKey, commandID); err != nil {
		slog.Error("failed to set command dedup", "error", err)
	}

	// Write audit entry (best-effort — don't fail the command if audit write fails)
	payloadJSON, _ := json.Marshal(params)
	if err := s.repo.InsertCommandAudit(ctx, &store.CommandAuditEntry{
		CommandID:      fmt.Sprintf("%d", commandID),
		RobotID:        robotID,
		TenantID:       tenantID,
		CommandType:    cmdType,
		Payload:        payloadJSON,
		Status:         "requested",
		IdempotencyKey: dedupKey,
	}); err != nil {
		slog.Error("failed to write command audit", "command_id", commandID, "error", err)
	}

	return &CommandResult{CommandID: commandID, Status: "queued", RobotID: robotID}, nil
}

// resolveViaInference calls the inference service /resolve endpoint to resolve
// a semantic instruction into a concrete command using FAISS vector search.
func (s *robotService) resolveViaInference(ctx context.Context, robotID, instruction string) (*command.Result, error) {
	endpoint := s.inferenceEndpoint
	if endpoint == "" {
		endpoint = "localhost:8081"
	}
	resolveURL := fmt.Sprintf("http://%s/resolve", endpoint)

	reqBody, err := json.Marshal(map[string]string{
		"instruction": instruction,
		"robot_id":    robotID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resolve request: %w", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(resolveURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("inference resolve unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("inference resolve returned %d", resp.StatusCode)
	}

	var result struct {
		Type   string         `json:"type"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode resolve response: %w", err)
	}
	if result.Type == "" {
		return nil, fmt.Errorf("inference could not resolve instruction")
	}

	return &command.Result{Type: result.Type, Params: result.Params}, nil
}

func (s *robotService) SemanticCommand(ctx context.Context, robotID, instruction, tenantID string) (*SemanticCommandResult, error) {
	s.cache.IncrementUsageCounter(ctx, tenantID, "semantic_commands")

	resolved := s.cmdReg.Resolve(instruction)

	// If no keyword matched, try FAISS-based resolution via inference service
	if resolved.Type == "semantic" {
		if resolvedFromInference, err := s.resolveViaInference(ctx, robotID, instruction); err == nil {
			resolved = *resolvedFromInference
			slog.Info("semantic command resolved via inference", "robot", robotID, "instruction", instruction, "resolved_type", resolved.Type)
		} else {
			slog.Warn("inference resolve failed, using raw semantic", "error", err, "instruction", instruction)
		}
	}

	// Idempotency check for semantic commands
	dedupKey := commandDedupKey(robotID, resolved.Type, resolved.Params)
	if existingID, err := s.cache.CheckCommandDedup(ctx, dedupKey); err != nil {
		slog.Error("semantic command dedup check failed", "error", err)
	} else if existingID != 0 {
		return &SemanticCommandResult{
			CommandID:   existingID,
			RobotID:     robotID,
			Status:      "duplicate",
			Interpreted: resolved,
			Original:    instruction,
		}, nil
	}

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

	if err := s.cache.SetCommandDedup(ctx, dedupKey, commandID); err != nil {
		slog.Error("failed to set semantic command dedup", "error", err)
	}

	// Write audit entry for semantic command
	payloadJSON, _ := json.Marshal(resolved.Params)
	if err := s.repo.InsertCommandAudit(ctx, &store.CommandAuditEntry{
		CommandID:      fmt.Sprintf("%d", commandID),
		RobotID:        robotID,
		TenantID:       tenantID,
		CommandType:    resolved.Type,
		Payload:        payloadJSON,
		Status:         "requested",
		Instruction:    instruction,
		IdempotencyKey: dedupKey,
	}); err != nil {
		slog.Error("failed to write semantic command audit", "command_id", commandID, "error", err)
	}

	return &SemanticCommandResult{
		CommandID:   commandID,
		RobotID:     robotID,
		Status:      "queued",
		Interpreted: resolved,
		Original:    instruction,
	}, nil
}

func (s *robotService) GetCommandHistory(ctx context.Context, robotID, tenantID string, limit int) ([]*store.CommandAuditEntry, error) {
	entries, err := s.repo.ListCommandAudit(ctx, robotID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("get command history for %s: %w", robotID, err)
	}
	return entries, nil
}
