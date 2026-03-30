package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// DeploymentEventConsumer reads deployment events from Kafka for SSE streaming.
type DeploymentEventConsumer interface {
	Consume(ctx context.Context, handler func(key, value []byte) error) error
}

// AgentHandler implements thin HTTP adapters for agent lifecycle management.
type AgentHandler struct {
	svc            service.AgentService
	cache          store.CacheStore
	deployConsumer DeploymentEventConsumer // Kafka consumer for deployment events (optional)
}

// NewAgentHandler creates a new AgentHandler.
func NewAgentHandler(svc service.AgentService, cache store.CacheStore) *AgentHandler {
	return &AgentHandler{svc: svc, cache: cache}
}

// SetDeploymentEventConsumer configures Kafka-based deployment event streaming.
func (h *AgentHandler) SetDeploymentEventConsumer(c DeploymentEventConsumer) {
	h.deployConsumer = c
}

// RegisterAgent handles POST /api/v1/agents.
func (h *AgentHandler) RegisterAgent(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())

	var req service.RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Version == "" {
		writeError(w, http.StatusBadRequest, "name and version are required")
		return
	}
	if len(req.SafetyEnvelope) == 0 {
		writeError(w, http.StatusBadRequest, "safety_envelope is required")
		return
	}

	agent, err := h.svc.RegisterAgent(r.Context(), tenantID, tenantID, req)
	if err != nil {
		slog.Error("register agent failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to register agent")
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

// GetAgent handles GET /api/v1/agents/{id}.
func (h *AgentHandler) GetAgent(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	agentID := r.PathValue("id")

	agent, err := h.svc.GetAgent(r.Context(), tenantID, agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// ListAgents handles GET /api/v1/agents.
func (h *AgentHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	agents, total, err := h.svc.ListAgents(r.Context(), tenantID, status, limit, offset)
	if err != nil {
		slog.Error("list agents failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agents": agents,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// DeployAgent handles POST /api/v1/agents/{id}/deploy.
func (h *AgentHandler) DeployAgent(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	agentID := r.PathValue("id")

	var req service.DeployAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	deployment, err := h.svc.DeployAgent(r.Context(), tenantID, agentID, tenantID, req)
	if err != nil {
		slog.Error("deploy agent failed", "agent", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to deploy agent")
		return
	}

	writeJSON(w, http.StatusAccepted, deployment)
}

// RollbackAgent handles POST /api/v1/agents/{id}/rollback.
func (h *AgentHandler) RollbackAgent(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	agentID := r.PathValue("id")

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	deployment, err := h.svc.RollbackAgent(r.Context(), tenantID, agentID, req.Reason, tenantID)
	if err != nil {
		slog.Error("rollback agent failed", "agent", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to rollback agent")
		return
	}

	writeJSON(w, http.StatusAccepted, deployment)
}

// GetDeployment handles GET /api/v1/deployments/{id}.
func (h *AgentHandler) GetDeployment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	deploymentID := r.PathValue("id")

	deployment, err := h.svc.GetDeployment(r.Context(), tenantID, deploymentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}

	writeJSON(w, http.StatusOK, deployment)
}

// ListDeployments handles GET /api/v1/agents/{id}/deployments.
func (h *AgentHandler) ListDeployments(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	agentID := r.PathValue("id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}

	deployments, err := h.svc.ListDeployments(r.Context(), tenantID, agentID, limit)
	if err != nil {
		slog.Error("list deployments failed", "agent", agentID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list deployments")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deployments": deployments,
		"total":       len(deployments),
	})
}

// StreamDeployment handles GET /api/v1/deployments/{id}/stream.
// Server-Sent Events stream for real-time deployment progress.
func (h *AgentHandler) StreamDeployment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.GetTenantID(r.Context())
	deploymentID := r.PathValue("id")

	// Verify deployment exists and belongs to tenant
	deployment, err := h.svc.GetDeployment(r.Context(), tenantID, deploymentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "deployment not found")
		return
	}

	// If deployment is already in a terminal state, send final event and close
	if deployment.Status == "complete" || deployment.Status == "rolled_back" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "event: %s\ndata: {\"deployment_id\":\"%s\",\"status\":\"%s\"}\n\n",
			deployment.Status, deploymentID, deployment.Status)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Send initial connected event
	fmt.Fprintf(w, "event: connected\ndata: {\"deployment_id\":\"%s\",\"status\":\"%s\"}\n\n",
		deploymentID, deployment.Status)
	flusher.Flush()

	// Stream deployment events — prefer Kafka, fall back to Redis pub/sub
	if h.deployConsumer != nil {
		h.streamDeploymentKafka(r.Context(), w, flusher, deploymentID)
	} else {
		h.streamDeploymentRedis(r.Context(), w, flusher, deploymentID)
	}
}

func (h *AgentHandler) streamDeploymentKafka(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, deploymentID string) {
	_ = h.deployConsumer.Consume(ctx, func(key, value []byte) error {
		// Filter: only events for this deployment
		if string(key) != deploymentID {
			return nil
		}

		var event struct {
			EventType string `json:"event_type"`
		}
		eventName := "update"
		if json.Unmarshal(value, &event) == nil && event.EventType != "" {
			eventName = event.EventType
		}

		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, string(value))
		flusher.Flush()

		if eventName == "complete" || eventName == "rolled_back" {
			return fmt.Errorf("terminal event") // break consume loop
		}
		return nil
	})
}

func (h *AgentHandler) streamDeploymentRedis(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, deploymentID string) {
	channel := fmt.Sprintf("deployment:%s:events", deploymentID)
	sub := h.cache.Subscribe(ctx, channel)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event struct {
				EventType string `json:"event_type"`
			}
			eventName := "update"
			if json.Unmarshal([]byte(msg.Payload), &event) == nil && event.EventType != "" {
				eventName = event.EventType
			}

			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, msg.Payload)
			flusher.Flush()

			if eventName == "complete" || eventName == "rolled_back" {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
