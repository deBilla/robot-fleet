package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// Default limits.
const DefaultSafetyIncidentLimit = 50

// ReportIncidentRequest holds the input for reporting a safety incident.
type ReportIncidentRequest struct {
	RobotID           string         `json:"robot_id"`
	AgentID           string         `json:"agent_id,omitempty"`
	DeploymentID      string         `json:"deployment_id,omitempty"`
	SiteID            string         `json:"site_id"`
	IncidentType      string         `json:"incident_type"`
	Severity          string         `json:"severity"`
	Details           map[string]any `json:"details"`
	TelemetrySnapshot map[string]any `json:"telemetry_snapshot,omitempty"`
}

// SafetyService manages safety incident tracking.
type SafetyService interface {
	ReportIncident(ctx context.Context, req ReportIncidentRequest) (*store.SafetyIncidentRecord, error)
	ListIncidents(ctx context.Context, severity, robotID string, limit int) ([]*store.SafetyIncidentRecord, error)
}

type safetyService struct {
	repo store.SafetyRepository
}

// NewSafetyService creates a new safety service.
func NewSafetyService(repo store.SafetyRepository) SafetyService {
	return &safetyService{repo: repo}
}

func (s *safetyService) ReportIncident(ctx context.Context, req ReportIncidentRequest) (*store.SafetyIncidentRecord, error) {
	if req.RobotID == "" || req.IncidentType == "" {
		return nil, fmt.Errorf("robot_id and incident_type are required")
	}
	severity := req.Severity
	if severity == "" {
		severity = "medium"
	}

	incident := &store.SafetyIncidentRecord{
		ID:                generateUUID(),
		RobotID:           req.RobotID,
		AgentID:           req.AgentID,
		DeploymentID:      req.DeploymentID,
		SiteID:            req.SiteID,
		IncidentType:      req.IncidentType,
		Severity:          severity,
		Details:           req.Details,
		TelemetrySnapshot: req.TelemetrySnapshot,
		CreatedAt:         time.Now().UTC(),
	}

	if incident.Details == nil {
		incident.Details = map[string]any{}
	}

	if err := s.repo.CreateSafetyIncident(ctx, incident); err != nil {
		return nil, fmt.Errorf("report incident: %w", err)
	}
	return incident, nil
}

func (s *safetyService) ListIncidents(ctx context.Context, severity, robotID string, limit int) ([]*store.SafetyIncidentRecord, error) {
	if limit <= 0 {
		limit = DefaultSafetyIncidentLimit
	}
	return s.repo.ListSafetyIncidents(ctx, severity, robotID, limit)
}
