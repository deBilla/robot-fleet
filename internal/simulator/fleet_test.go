package simulator

import (
	"testing"
	"time"
)

func TestNewFleet(t *testing.T) {
	f := NewFleet(5, 100*time.Millisecond, "localhost:50051", "")

	if len(f.robots) != 5 {
		t.Errorf("expected 5 robots, got %d", len(f.robots))
	}
	if f.tickInterval != 100*time.Millisecond {
		t.Errorf("expected 100ms tick, got %v", f.tickInterval)
	}
	if f.grpcTarget != "localhost:50051" {
		t.Errorf("expected localhost:50051, got %s", f.grpcTarget)
	}
}

func TestNewFleet_UniqueRobots(t *testing.T) {
	f := NewFleet(10, 100*time.Millisecond, "localhost:50051", "")

	ids := make(map[string]bool)
	for _, r := range f.robots {
		if ids[r.ID] {
			t.Errorf("duplicate robot ID: %s", r.ID)
		}
		ids[r.ID] = true
	}
}

func TestNewFleet_ZeroRobots(t *testing.T) {
	f := NewFleet(0, 100*time.Millisecond, "localhost:50051", "")

	if len(f.robots) != 0 {
		t.Errorf("expected 0 robots, got %d", len(f.robots))
	}
}

func TestNewFleet_LargeFleet(t *testing.T) {
	f := NewFleet(100, 100*time.Millisecond, "localhost:50051", "")

	if len(f.robots) != 100 {
		t.Errorf("expected 100 robots, got %d", len(f.robots))
	}

	// Verify all robots have valid state
	for _, r := range f.robots {
		if r.Battery < 0.8 || r.Battery > 1.0 {
			t.Errorf("robot %s has invalid battery: %f", r.ID, r.Battery)
		}
		if r.Status != "active" {
			t.Errorf("robot %s should start active, got %s", r.ID, r.Status)
		}
	}
}
