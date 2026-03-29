package store

import (
	"encoding/json"
	"testing"
)

func TestRobotHotState_JSONRoundTrip(t *testing.T) {
	state := &RobotHotState{
		RobotID:      "robot-0001",
		Status:       "active",
		PosX:         1.5,
		PosY:         -2.3,
		PosZ:         0.0,
		BatteryLevel: 0.85,
		LastSeen:     1711612800,
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RobotHotState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.RobotID != state.RobotID {
		t.Errorf("expected %s, got %s", state.RobotID, decoded.RobotID)
	}
	if decoded.Status != state.Status {
		t.Errorf("expected %s, got %s", state.Status, decoded.Status)
	}
	if decoded.PosX != state.PosX {
		t.Errorf("expected %f, got %f", state.PosX, decoded.PosX)
	}
	if decoded.BatteryLevel != state.BatteryLevel {
		t.Errorf("expected %f, got %f", state.BatteryLevel, decoded.BatteryLevel)
	}
}

func TestRobotKey(t *testing.T) {
	key := robotKey("robot-0001")
	expected := "robot:state:robot-0001"
	if key != expected {
		t.Errorf("expected %s, got %s", expected, key)
	}
}

func TestRobotRecord_Fields(t *testing.T) {
	r := &RobotRecord{
		ID:       "robot-test",
		Name:     "Test Robot",
		Model:    "humanoid-v1",
		Status:   "active",
		TenantID: "tenant-1",
	}

	if r.ID != "robot-test" {
		t.Errorf("unexpected ID: %s", r.ID)
	}
	if r.TenantID != "tenant-1" {
		t.Errorf("unexpected TenantID: %s", r.TenantID)
	}
}
