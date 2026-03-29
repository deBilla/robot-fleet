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
	return body, nil
}
