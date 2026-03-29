package validation

import (
	"context"
	"encoding/json"
	"testing"

	pb "github.com/dimuthu/robot-fleet-playground/internal/simulation"
)

func TestValidateAgent_StandardSuite(t *testing.T) {
	server := NewServer()

	envelope, _ := json.Marshal(map[string]any{
		"max_velocity": 10.0,
		"max_force":    100.0,
	})

	report, err := server.ValidateAgent(context.Background(), &pb.ValidationRequest{
		AgentId:            "agent-test-001",
		AgentVersion:       "1.0.0",
		SafetyEnvelopeJson: envelope,
		ScenarioSuite:      "standard",
		MaxScenarios:       5,
	})
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	if report.ScenariosTotal == 0 {
		t.Error("should have run at least one scenario")
	}
	if report.ScenariosTotal > 5 {
		t.Errorf("should respect max_scenarios=5, got %d", report.ScenariosTotal)
	}
	if report.PassRate < 0 || report.PassRate > 1 {
		t.Errorf("pass_rate should be between 0 and 1, got %f", report.PassRate)
	}
	if report.RequestId == "" {
		t.Error("request_id should not be empty")
	}

	t.Logf("validation: passed=%d/%d, rate=%.1f%%, approved=%v",
		report.ScenariosPassed, report.ScenariosTotal,
		report.PassRate*100, report.DeploymentApproved)
}

func TestValidateAgent_StrictEnvelope(t *testing.T) {
	server := NewServer()

	// Very strict force limit — should cause some violations
	envelope, _ := json.Marshal(map[string]any{
		"max_velocity": 0.001,
		"max_force":    0.001,
	})

	report, err := server.ValidateAgent(context.Background(), &pb.ValidationRequest{
		AgentId:            "agent-strict",
		AgentVersion:       "1.0.0",
		SafetyEnvelopeJson: envelope,
		ScenarioSuite:      "standard",
		MaxScenarios:       3,
	})
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// With extremely strict limits, some scenarios should fail
	t.Logf("strict envelope: passed=%d/%d, rate=%.1f%%",
		report.ScenariosPassed, report.ScenariosTotal, report.PassRate*100)
}

func TestValidateAgent_EmptyEnvelope(t *testing.T) {
	server := NewServer()

	report, err := server.ValidateAgent(context.Background(), &pb.ValidationRequest{
		AgentId:       "agent-no-envelope",
		AgentVersion:  "1.0.0",
		ScenarioSuite: "standard",
		MaxScenarios:  3,
	})
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// No envelope = no safety violations = all pass
	if report.ScenariosPassed != report.ScenariosTotal {
		t.Errorf("with no envelope constraints, all should pass: %d/%d",
			report.ScenariosPassed, report.ScenariosTotal)
	}
}

func TestListScenarios_All(t *testing.T) {
	server := NewServer()

	resp, err := server.ListScenarios(context.Background(), &pb.ListScenariosRequest{})
	if err != nil {
		t.Fatalf("list scenarios failed: %v", err)
	}

	if len(resp.Suites) == 0 {
		t.Error("should have at least one suite")
	}

	totalScenarios := 0
	for _, suite := range resp.Suites {
		if suite.Name == "" {
			t.Error("suite name should not be empty")
		}
		if len(suite.Scenarios) == 0 {
			t.Errorf("suite %s should have scenarios", suite.Name)
		}
		totalScenarios += len(suite.Scenarios)
	}

	if totalScenarios != len(scenarioDefs) {
		t.Errorf("expected %d total scenarios, got %d", len(scenarioDefs), totalScenarios)
	}
}

func TestListScenarios_FilterBySuite(t *testing.T) {
	server := NewServer()

	resp, err := server.ListScenarios(context.Background(), &pb.ListScenariosRequest{
		Suite: "stress",
	})
	if err != nil {
		t.Fatalf("list scenarios failed: %v", err)
	}

	if len(resp.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(resp.Suites))
	}
	if resp.Suites[0].Name != "stress" {
		t.Errorf("expected stress suite, got %s", resp.Suites[0].Name)
	}

	for _, sc := range resp.Suites[0].Scenarios {
		if sc.Id == "" {
			t.Error("scenario id should not be empty")
		}
		if sc.Description == "" {
			t.Error("scenario description should not be empty")
		}
	}
}
