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
	"github.com/dimuthu/robot-fleet/internal/middleware"
)

const (
	DefaultModelID    = "groot-n1-v1.5"
	DefaultEmbodiment = "humanoid-v1"
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

	if req.ModelID == "" {
		req.ModelID = DefaultModelID
	}
	if req.Embodiment == "" {
		req.Embodiment = DefaultEmbodiment
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

	// If a robot_id was provided, forward predicted actions to the simulator via Redis
	if req.RobotID != "" {
		go s.forwardInferenceToRobot(context.Background(), req.RobotID, body)
	}

	return body, nil
}

// forwardInferenceToRobot parses inference predicted_actions and publishes
// them as an apply_actions command to the simulator via Redis.
func (s *robotService) forwardInferenceToRobot(ctx context.Context, robotID string, inferenceBody []byte) {
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

	cmdData, err := json.Marshal(map[string]any{
		"robot_id": robotID,
		"command": map[string]any{
			"type":   "apply_actions",
			"params": map[string]any{"actions": actions},
		},
		"issued_at": time.Now().UTC(),
	})
	if err != nil {
		slog.Error("failed to marshal inference actions", "error", err)
		return
	}

	if err := s.cache.PublishEvent(ctx, "commands:"+robotID, cmdData); err != nil {
		slog.Error("failed to publish inference actions", "robot", robotID, "error", err)
	} else {
		slog.Info("forwarded inference actions to robot", "robot", robotID, "joints", len(result.PredictedActions))
	}
}
