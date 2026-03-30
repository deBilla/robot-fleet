package command

import "strings"

// KnownLocomotionCommands are command types the robot has built-in
// time-varying action loops for. Sending these directly produces
// smoother, continuous motion than sending single-frame torques.
var KnownLocomotionCommands = map[string]bool{
	"wave": true, "dance": true, "walk": true, "move": true,
	"jump": true, "bow": true, "sit": true, "stop": true,
	"crouch": true, "look_around": true, "stretch": true,
	"stand": true, "idle": true,
}

// MatchLocomotionCommand extracts a known command type from a free-text instruction.
// Returns the matched command name, or empty string if no match.
func MatchLocomotionCommand(instruction string) string {
	inst := strings.ToLower(instruction)
	for cmd := range KnownLocomotionCommands {
		if strings.Contains(inst, cmd) {
			return cmd
		}
	}
	return ""
}
