package command

import "testing"

func TestDefaultRegistry(t *testing.T) {
	reg := DefaultRegistry()

	tests := []struct {
		instruction string
		wantType    string
	}{
		{"stop moving", "stop"},
		{"halt now", "stop"},
		{"emergency stop", "stop"},
		{"do a dance", "dance"},
		{"wave hello", "wave"},
		{"say hello", "wave"},
		{"greet the visitor", "wave"},
		{"bow down", "bow"},
		{"show respect", "bow"},
		{"sit down", "sit"},
		{"crouch low", "sit"},
		{"jump up", "jump"},
		{"hop around", "jump"},
		{"look around", "look_around"},
		{"scan the area", "look_around"},
		{"search for objects", "look_around"},
		{"stretch your arms", "stretch"},
		{"warm up", "stretch"},
		{"move forward", "move_relative"},
		{"go ahead", "move_relative"},
		{"go back", "move_relative"},
		{"go to the table", "move"},
		{"move to position", "move"},
		{"do something unknown", "semantic"},
	}

	for _, tt := range tests {
		t.Run(tt.instruction, func(t *testing.T) {
			result := reg.Resolve(tt.instruction)
			if result.Type != tt.wantType {
				t.Errorf("Resolve(%q) = %q, want %q", tt.instruction, result.Type, tt.wantType)
			}
			if result.Params == nil {
				t.Errorf("Resolve(%q) returned nil params", tt.instruction)
			}
		})
	}
}

func TestStopEmergencyParam(t *testing.T) {
	reg := DefaultRegistry()

	normal := reg.Resolve("stop moving")
	if normal.Params["emergency"] != false {
		t.Error("expected emergency=false for 'stop moving'")
	}

	emergency := reg.Resolve("emergency stop now")
	if emergency.Params["emergency"] != true {
		t.Error("expected emergency=true for 'emergency stop now'")
	}
}

func TestMoveRelativeDirection(t *testing.T) {
	reg := DefaultRegistry()

	fwd := reg.Resolve("move forward")
	if fwd.Params["direction"] != "forward" {
		t.Errorf("expected direction=forward, got %v", fwd.Params["direction"])
	}

	back := reg.Resolve("go back")
	if back.Params["direction"] != "backward" {
		t.Errorf("expected direction=backward, got %v", back.Params["direction"])
	}
}

func TestFallbackPreservesOriginalCase(t *testing.T) {
	reg := DefaultRegistry()
	result := reg.Resolve("Do Something WEIRD")
	if result.Params["instruction"] != "Do Something WEIRD" {
		t.Errorf("expected original case preserved, got %v", result.Params["instruction"])
	}
}

func TestCustomMatcher(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&KeywordMatcher{Keywords: []string{"custom"}, CmdType: "custom_action"})

	result := reg.Resolve("do a custom thing")
	if result.Type != "custom_action" {
		t.Errorf("expected custom_action, got %s", result.Type)
	}

	// Unmatched falls through to semantic
	result = reg.Resolve("nothing matches")
	if result.Type != "semantic" {
		t.Errorf("expected semantic fallback, got %s", result.Type)
	}
}
