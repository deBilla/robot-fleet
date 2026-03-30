package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/dimuthu/robot-fleet/internal/circuitbreaker"
	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/middleware"
)

const (
	DefaultModelID    = "" // empty = inference server uses its active model
	DefaultEmbodiment = ""
	DefaultTimeout    = 5 * time.Second
)

// inferenceBreaker protects the inference service from cascading failures.
var inferenceBreaker = circuitbreaker.New(circuitbreaker.Config{
	FailureThreshold: 5,
	SuccessThreshold: 2,
	Timeout:          30 * time.Second,
})

func (s *robotService) RunInference(ctx context.Context, req InferenceRequest, tenantID string) ([]byte, error) {
	s.cache.IncrementUsageCounter(ctx, tenantID, "inference_calls")

	// Look up the robot's assigned inference model if not explicitly provided
	if req.RobotID != "" && req.ModelID == "" {
		robot, err := s.repo.GetRobot(ctx, req.RobotID)
		if err == nil && robot.InferenceModelID != "" {
			req.ModelID = robot.InferenceModelID
		}
	}

	endpoint := s.inferenceEndpoint
	if endpoint == "" {
		endpoint = "localhost:8081"
	}
	inferenceURL := fmt.Sprintf("http://%s/predict", endpoint)

	reqBody, err := json.Marshal(map[string]string{
		"image":       req.Image,
		"instruction": req.Instruction,
		"model_id":    req.ModelID,
		"embodiment":  req.Embodiment,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal inference request: %w", err)
	}

	timeout := s.inferenceTimeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	client := &http.Client{Timeout: timeout}

	var body []byte
	start := time.Now()

	cbErr := inferenceBreaker.Execute(func() error {
		resp, err := client.Post(inferenceURL, "application/json", bytes.NewReader(reqBody))
		if err != nil {
			return fmt.Errorf("inference service unavailable: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			return fmt.Errorf("inference service returned %d", resp.StatusCode)
		}

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read inference response: %w", err)
		}
		return nil
	})

	if cbErr != nil {
		slog.Error("inference call failed", "error", cbErr, "url", inferenceURL)
		return nil, cbErr
	}

	middleware.InferenceDuration.Observe(time.Since(start).Seconds())

	// If a robot_id was provided, forward as a command to the robot.
	// For known locomotion commands (wave, dance, walk, etc.) send the command
	// type directly so the robot runs its own time-varying action loop.
	// For unknown instructions, send apply_actions with the predicted torques.
	if req.RobotID != "" {
		go s.forwardInferenceToRobot(context.Background(), req.RobotID, req.Instruction, body)
	}

	return body, nil
}

// forwardInferenceToRobot sends the inference result to the robot as a command.
// When a Kafka command producer is available, it publishes a CommandMessage so the
// processor picks it up and starts a Temporal workflow with full audit trail.
// Falls back to direct Redis pub/sub when Kafka is unavailable.
func (s *robotService) forwardInferenceToRobot(ctx context.Context, robotID, instruction string, inferenceBody []byte) {
	// Resolve command type: known locomotion command or apply_actions with torques
	cmdType := command.MatchLocomotionCommand(instruction)
	params := map[string]any{}

	if cmdType == "" {
		// Unknown instruction: extract predicted torques
		var result struct {
			PredictedActions []struct {
				Joint  string  `json:"joint"`
				Torque float64 `json:"torque"`
			} `json:"predicted_actions"`
		}
		if err := json.Unmarshal(inferenceBody, &result); err != nil || len(result.PredictedActions) == 0 {
			return
		}
		actions := make([]map[string]any, 0, len(result.PredictedActions))
		for _, a := range result.PredictedActions {
			actions = append(actions, map[string]any{"joint": a.Joint, "torque": a.Torque})
		}
		cmdType = "apply_actions"
		params = map[string]any{"actions": actions}
	}

	// Kafka path: publish CommandMessage for Temporal workflow orchestration
	if s.commandProducer != nil {
		msg := CommandMessage{
			RobotID:   robotID,
			CommandID: time.Now().UnixNano(),
			CmdType:   cmdType,
			Params:    params,
			TenantID:  "tenant-dev",
			DedupKey:  commandDedupKey(robotID, cmdType, params),
		}
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("failed to marshal command message", "error", err)
			return
		}
		if err := s.commandProducer.Publish(robotID, data); err != nil {
			slog.Error("failed to publish command to kafka", "robot", robotID, "error", err)
		} else {
			slog.Info("forwarded inference command via kafka", "robot", robotID, "command", cmdType)
		}
		return
	}

	// Legacy fallback: direct Redis pub/sub
	cmdData, err := json.Marshal(map[string]any{
		"robot_id": robotID,
		"command":  map[string]any{"type": cmdType, "params": params},
		"issued_at": time.Now().UTC(),
	})
	if err != nil {
		slog.Error("failed to marshal command", "error", err)
		return
	}
	if err := s.cache.PublishEvent(ctx, "commands:"+robotID, cmdData); err != nil {
		slog.Error("failed to publish command", "robot", robotID, "error", err)
	} else {
		slog.Info("forwarded inference command via redis", "robot", robotID, "command", cmdType)
	}
}
