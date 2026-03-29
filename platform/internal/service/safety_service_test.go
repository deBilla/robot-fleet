package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// --- Mock Safety Repository ---

type mockSafetyRepo struct {
	incidents []*store.SafetyIncidentRecord
	err       error
}

func (m *mockSafetyRepo) CreateSafetyIncident(_ context.Context, i *store.SafetyIncidentRecord) error {
	if m.err != nil {
		return m.err
	}
	m.incidents = append(m.incidents, i)
	return nil
}

func (m *mockSafetyRepo) ListSafetyIncidents(_ context.Context, severity, robotID string, limit int) ([]*store.SafetyIncidentRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	var filtered []*store.SafetyIncidentRecord
	for _, i := range m.incidents {
		if severity != "" && i.Severity != severity {
			continue
		}
		if robotID != "" && i.RobotID != robotID {
			continue
		}
		filtered = append(filtered, i)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

// --- Tests ---

func TestReportIncident(t *testing.T) {
	tests := []struct {
		name    string
		req     ReportIncidentRequest
		wantErr bool
	}{
		{
			name: "valid incident",
			req: ReportIncidentRequest{
				RobotID:      "robot-001",
				IncidentType: "velocity_exceeded",
				Severity:     "high",
				SiteID:       "warehouse-a",
				Details:      map[string]any{"velocity": 2.1, "limit": 1.5},
			},
			wantErr: false,
		},
		{
			name: "defaults severity to medium",
			req: ReportIncidentRequest{
				RobotID:      "robot-002",
				IncidentType: "force_exceeded",
			},
			wantErr: false,
		},
		{
			name: "missing robot_id",
			req: ReportIncidentRequest{
				IncidentType: "force_exceeded",
			},
			wantErr: true,
		},
		{
			name: "missing incident_type",
			req: ReportIncidentRequest{
				RobotID: "robot-001",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockSafetyRepo{}
			svc := NewSafetyService(repo)

			incident, err := svc.ReportIncident(context.Background(), tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if incident.ID == "" {
				t.Error("incident ID should not be empty")
			}
			if incident.RobotID != tt.req.RobotID {
				t.Errorf("expected robot %s, got %s", tt.req.RobotID, incident.RobotID)
			}
			if tt.req.Severity == "" && incident.Severity != "medium" {
				t.Errorf("expected default severity medium, got %s", incident.Severity)
			}
		})
	}
}

func TestListIncidents_Filtering(t *testing.T) {
	repo := &mockSafetyRepo{}
	svc := NewSafetyService(repo)

	// Create mixed incidents
	for i, sev := range []string{"critical", "high", "medium", "low", "critical"} {
		svc.ReportIncident(context.Background(), ReportIncidentRequest{
			RobotID:      fmt.Sprintf("robot-%03d", i),
			IncidentType: "test",
			Severity:     sev,
		})
	}

	// Filter by severity
	critical, err := svc.ListIncidents(context.Background(), "critical", "", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(critical) != 2 {
		t.Errorf("expected 2 critical, got %d", len(critical))
	}

	// Filter by robot
	byRobot, err := svc.ListIncidents(context.Background(), "", "robot-001", 50)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(byRobot) != 1 {
		t.Errorf("expected 1 for robot-001, got %d", len(byRobot))
	}

	// Default limit
	all, err := svc.ListIncidents(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5, got %d", len(all))
	}
}
