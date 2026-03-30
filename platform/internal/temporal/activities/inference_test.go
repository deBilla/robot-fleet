package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunInference_KnownLocomotionCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"predicted_actions": []map[string]any{
				{"joint": "left_hip_yaw", "torque": 0.5},
			},
			"confidence": 0.9,
		})
	}))
	defer server.Close()

	acts := &InferenceActivities{
		Endpoint: server.Listener.Addr().String(),
		Timeout:  5 * time.Second,
	}

	result, err := acts.RunInference(context.Background(), InferenceInput{
		Instruction: "walk forward",
		ModelID:     "groot-n1-v1.5",
		Embodiment:  "humanoid-v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "walk" is a known locomotion command — should resolve directly
	if result.CmdType != "walk" {
		t.Errorf("expected CmdType=walk, got %s", result.CmdType)
	}
	if len(result.Params) != 0 {
		t.Errorf("expected empty params for locomotion command, got %v", result.Params)
	}
}

func TestRunInference_UnknownInstruction_ApplyActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"predicted_actions": []map[string]any{
				{"joint": "left_hip_yaw", "torque": 0.3},
				{"joint": "right_knee", "torque": -0.5},
			},
			"confidence": 0.7,
		})
	}))
	defer server.Close()

	acts := &InferenceActivities{
		Endpoint: server.Listener.Addr().String(),
		Timeout:  5 * time.Second,
	}

	result, err := acts.RunInference(context.Background(), InferenceInput{
		Instruction: "do a backflip with style",
		ModelID:     "groot-n1-v1.5",
		Embodiment:  "humanoid-v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CmdType != "apply_actions" {
		t.Errorf("expected CmdType=apply_actions, got %s", result.CmdType)
	}
	actions, ok := result.Params["actions"].([]map[string]any)
	if !ok {
		t.Fatalf("expected actions in params, got %v", result.Params)
	}
	if len(actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(actions))
	}
}

func TestRunInference_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	acts := &InferenceActivities{
		Endpoint: server.Listener.Addr().String(),
		Timeout:  5 * time.Second,
	}

	_, err := acts.RunInference(context.Background(), InferenceInput{
		Instruction: "walk",
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRunInference_RequestFormat(t *testing.T) {
	var receivedBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)
		json.NewEncoder(w).Encode(map[string]any{
			"predicted_actions": []map[string]any{},
		})
	}))
	defer server.Close()

	acts := &InferenceActivities{
		Endpoint: server.Listener.Addr().String(),
		Timeout:  5 * time.Second,
	}

	acts.RunInference(context.Background(), InferenceInput{
		Image:       "base64data",
		Instruction: "wave hello",
		ModelID:     "test-model",
		Embodiment:  "test-body",
	})

	if receivedBody["instruction"] != "wave hello" {
		t.Errorf("expected instruction=wave hello, got %s", receivedBody["instruction"])
	}
	if receivedBody["model_id"] != "test-model" {
		t.Errorf("expected model_id=test-model, got %s", receivedBody["model_id"])
	}
	if receivedBody["image"] != "base64data" {
		t.Errorf("expected image=base64data, got %s", receivedBody["image"])
	}
}
