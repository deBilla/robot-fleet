package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dimuthu/robot-fleet/internal/command"
)

// InferenceActivities holds dependencies for inference-related Temporal activities.
type InferenceActivities struct {
	Endpoint string
	Timeout  time.Duration
}

// InferenceInput is the input for the RunInference activity.
type InferenceInput struct {
	Image       string `json:"image"`
	Instruction string `json:"instruction"`
	ModelID     string `json:"model_id"`
	Embodiment  string `json:"embodiment"`
}

// InferenceResult is the resolved command from inference.
type InferenceResult struct {
	CmdType string         `json:"cmd_type"`
	Params  map[string]any `json:"params"`
}

// RunInference calls the inference service and resolves the result into a command.
// For known locomotion commands (walk, wave, etc.) it returns the command type directly.
// For unknown instructions, it returns apply_actions with predicted torques.
func (a *InferenceActivities) RunInference(ctx context.Context, input InferenceInput) (*InferenceResult, error) {
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = "localhost:8081"
	}
	inferenceURL := fmt.Sprintf("http://%s/predict", endpoint)

	timeout := a.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	reqBody, err := json.Marshal(map[string]string{
		"image":       input.Image,
		"instruction": input.Instruction,
		"model_id":    input.ModelID,
		"embodiment":  input.Embodiment,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal inference request: %w", err)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(inferenceURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("inference service unavailable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("inference service returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read inference response: %w", err)
	}

	// Check if instruction matches a known locomotion command
	if cmdType := command.MatchLocomotionCommand(input.Instruction); cmdType != "" {
		return &InferenceResult{CmdType: cmdType, Params: map[string]any{}}, nil
	}

	// Unknown instruction: extract predicted actions
	var inferResp struct {
		PredictedActions []struct {
			Joint  string  `json:"joint"`
			Torque float64 `json:"torque"`
		} `json:"predicted_actions"`
	}
	if err := json.Unmarshal(body, &inferResp); err != nil || len(inferResp.PredictedActions) == 0 {
		return &InferenceResult{CmdType: "idle", Params: map[string]any{}}, nil
	}

	actions := make([]map[string]any, 0, len(inferResp.PredictedActions))
	for _, a := range inferResp.PredictedActions {
		actions = append(actions, map[string]any{"joint": a.Joint, "torque": a.Torque})
	}

	return &InferenceResult{
		CmdType: "apply_actions",
		Params:  map[string]any{"actions": actions},
	}, nil
}
