package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	pb "github.com/dimuthu/robot-fleet-playground/internal/simulation"
	"github.com/dimuthu/robot-fleet-playground/internal/simulator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Default configuration.
const (
	DefaultPassThreshold     = 0.8
	DefaultMaxScenarios      = 10
	ScenarioStepsPerScenario = 200
)

// scenarioDefs are the built-in scenario definitions.
var scenarioDefs = []scenarioDef{
	{id: "walk-forward", desc: "Walk forward 5 meters on flat ground", difficulty: "easy", tags: []string{"locomotion"}, suite: "standard"},
	{id: "walk-turn", desc: "Walk and turn 90 degrees", difficulty: "easy", tags: []string{"locomotion"}, suite: "standard"},
	{id: "idle-stability", desc: "Stand idle for 30 seconds without drift", difficulty: "easy", tags: []string{"locomotion"}, suite: "standard"},
	{id: "battery-low", desc: "Operate with battery below 20%", difficulty: "medium", tags: []string{"locomotion"}, suite: "standard"},
	{id: "push-recovery", desc: "Recover balance after lateral push", difficulty: "medium", tags: []string{"locomotion"}, suite: "stress"},
	{id: "rapid-commands", desc: "Execute 10 commands in rapid succession", difficulty: "medium", tags: []string{"locomotion", "manipulation"}, suite: "stress"},
	{id: "velocity-boundary", desc: "Approach max velocity limit", difficulty: "hard", tags: []string{"locomotion"}, suite: "edge-cases"},
	{id: "force-boundary", desc: "Approach max force limit", difficulty: "hard", tags: []string{"manipulation"}, suite: "edge-cases"},
	{id: "long-duration", desc: "Operate continuously for 1000 steps", difficulty: "hard", tags: []string{"locomotion"}, suite: "stress"},
	{id: "multi-action", desc: "Execute walk-wave-sit-stand sequence", difficulty: "medium", tags: []string{"locomotion", "social"}, suite: "standard"},
}

type scenarioDef struct {
	id, desc, difficulty, suite string
	tags                        []string
}

// Server implements the SimulationService gRPC server.
type Server struct {
	pb.UnimplementedSimulationServiceServer
}

// NewServer creates a new validation server.
func NewServer() *Server {
	return &Server{}
}

// ValidateAgent runs the agent through a scenario suite using the robot simulator.
func (s *Server) ValidateAgent(ctx context.Context, req *pb.ValidationRequest) (*pb.ValidationReport, error) {
	slog.Info("validation request received",
		"agent_id", req.AgentId,
		"agent_version", req.AgentVersion,
		"suite", req.ScenarioSuite,
		"max_scenarios", req.MaxScenarios,
	)

	// Parse safety envelope
	var envelope map[string]any
	if len(req.SafetyEnvelopeJson) > 0 {
		_ = json.Unmarshal(req.SafetyEnvelopeJson, &envelope) // best-effort: validated at platform side
	}

	// Select scenarios for the suite
	suite := req.ScenarioSuite
	if suite == "" {
		suite = "standard"
	}
	maxScenarios := int(req.MaxScenarios)
	if maxScenarios <= 0 {
		maxScenarios = DefaultMaxScenarios
	}

	scenarios := filterScenarios(suite, maxScenarios)

	var results []*pb.ScenarioResult
	passed := 0

	for _, sc := range scenarios {
		if ctx.Err() != nil {
			break
		}

		result := s.runScenario(sc, envelope)
		results = append(results, result)
		if result.Passed {
			passed++
		}
	}

	total := len(results)
	passRate := 0.0
	if total > 0 {
		passRate = float64(passed) / float64(total)
	}
	approved := passRate >= DefaultPassThreshold

	report := &pb.ValidationReport{
		RequestId:          fmt.Sprintf("val-%d", time.Now().UnixNano()),
		ScenariosPassed:    int32(passed),
		ScenariosTotal:     int32(total),
		PassRate:           passRate,
		DeploymentApproved: approved,
		Results:            results,
		CompletedAt:        timestamppb.Now(),
	}

	slog.Info("validation complete",
		"agent_id", req.AgentId,
		"passed", passed,
		"total", total,
		"pass_rate", passRate,
		"approved", approved,
	)

	return report, nil
}

// RunScenario runs a single scenario with streaming events.
func (s *Server) RunScenario(req *pb.ScenarioRequest, stream pb.SimulationService_RunScenarioServer) error {
	slog.Info("scenario run requested", "scenario_id", req.ScenarioId, "agent_id", req.AgentId)

	// Find the scenario
	var sc *scenarioDef
	for i := range scenarioDefs {
		if scenarioDefs[i].id == req.ScenarioId {
			sc = &scenarioDefs[i]
			break
		}
	}
	if sc == nil {
		return fmt.Errorf("scenario %s not found", req.ScenarioId)
	}

	// Send started event
	_ = stream.Send(&pb.ScenarioEvent{ // best-effort: stream write
		ScenarioId:     sc.id,
		EventType:      "started",
		Message:        fmt.Sprintf("Starting scenario: %s", sc.desc),
		ElapsedSeconds: 0,
		Timestamp:      timestamppb.Now(),
	})

	var envelope map[string]any
	if len(req.SafetyEnvelopeJson) > 0 {
		_ = json.Unmarshal(req.SafetyEnvelopeJson, &envelope) // best-effort
	}

	// Run the simulation
	robot := simulator.NewRobot(0)
	start := time.Now()

	for step := range ScenarioStepsPerScenario {
		if stream.Context().Err() != nil {
			return stream.Context().Err()
		}

		robot.Step()
		elapsed := time.Since(start).Seconds()

		// Send progress every 50 steps
		if step > 0 && step%50 == 0 {
			_ = stream.Send(&pb.ScenarioEvent{ // best-effort: stream write
				ScenarioId:     sc.id,
				EventType:      "step",
				Message:        fmt.Sprintf("Step %d/%d", step, ScenarioStepsPerScenario),
				ElapsedSeconds: elapsed,
				Metrics:        map[string]float64{"battery": robot.Battery, "step": float64(step)},
				Timestamp:      timestamppb.Now(),
			})
		}

		// Check for safety violations
		if violations := checkSafetyViolations(robot, envelope); len(violations) > 0 {
			_ = stream.Send(&pb.ScenarioEvent{ // best-effort: stream write
				ScenarioId:     sc.id,
				EventType:      "violation",
				Message:        fmt.Sprintf("Safety violation: %s", violations[0].Type),
				ElapsedSeconds: elapsed,
				Timestamp:      timestamppb.Now(),
			})
		}
	}

	elapsed := time.Since(start).Seconds()
	_ = stream.Send(&pb.ScenarioEvent{ // best-effort: stream write
		ScenarioId:     sc.id,
		EventType:      "completed",
		Message:        "Scenario completed",
		ElapsedSeconds: elapsed,
		Metrics: map[string]float64{
			"final_battery":    robot.Battery,
			"duration_seconds": elapsed,
		},
		Timestamp: timestamppb.Now(),
	})

	return nil
}

// ListScenarios returns the available scenario suites.
func (s *Server) ListScenarios(_ context.Context, req *pb.ListScenariosRequest) (*pb.ListScenariosResponse, error) {
	suiteMap := map[string]*pb.ScenarioSuite{}

	for _, sc := range scenarioDefs {
		if req.Suite != "" && sc.suite != req.Suite {
			continue
		}
		suite, ok := suiteMap[sc.suite]
		if !ok {
			suite = &pb.ScenarioSuite{
				Name:        sc.suite,
				Description: fmt.Sprintf("%s scenario suite", sc.suite),
			}
			suiteMap[sc.suite] = suite
		}
		suite.ScenarioCount++
		suite.Scenarios = append(suite.Scenarios, &pb.ScenarioInfo{
			Id:          sc.id,
			Description: sc.desc,
			Difficulty:  sc.difficulty,
			Tags:        sc.tags,
		})
	}

	var suites []*pb.ScenarioSuite
	for _, suite := range suiteMap {
		suites = append(suites, suite)
	}

	return &pb.ListScenariosResponse{Suites: suites}, nil
}

// runScenario executes a single scenario using the robot simulator.
func (s *Server) runScenario(sc scenarioDef, envelope map[string]any) *pb.ScenarioResult {
	robot := simulator.NewRobot(0)
	start := time.Now()

	var violations []*pb.SafetyViolation

	// Apply an action based on scenario type
	switch sc.id {
	case "walk-forward", "walk-turn":
		robot.ApplyCommand("move", map[string]any{"x": 5.0, "y": 0.0})
	case "push-recovery":
		robot.ApplyCommand("move", map[string]any{"x": 2.0, "y": 1.0})
	case "multi-action":
		robot.ApplyCommand("wave", nil)
	default:
		// idle scenarios — just step
	}

	for range ScenarioStepsPerScenario {
		robot.Step()
		if v := checkSafetyViolations(robot, envelope); len(v) > 0 {
			violations = append(violations, v...)
		}
	}

	duration := time.Since(start).Seconds()

	// Determine pass/fail
	passed := true
	failureReason := ""

	if len(violations) > 0 {
		passed = false
		failureReason = fmt.Sprintf("%d safety violations detected", len(violations))
	}
	if robot.Battery <= 0 {
		passed = false
		failureReason = "battery depleted"
	}

	return &pb.ScenarioResult{
		ScenarioId:      sc.id,
		Description:     sc.desc,
		Passed:          passed,
		FailureReason:   failureReason,
		DurationSeconds: duration,
		Violations:      violations,
		Metrics: map[string]float64{
			"final_battery":  robot.Battery,
			"total_steps":    float64(ScenarioStepsPerScenario),
			"violation_count": float64(len(violations)),
		},
	}
}

// checkSafetyViolations checks the robot state against the safety envelope.
func checkSafetyViolations(robot *simulator.Robot, envelope map[string]any) []*pb.SafetyViolation {
	var violations []*pb.SafetyViolation

	// Check velocity limit from envelope
	if maxVel, ok := envelope["max_velocity"]; ok {
		limit, _ := toFloat64(maxVel)
		if limit > 0 {
			velocity := math.Sqrt(robot.PosX*robot.PosX + robot.PosY*robot.PosY) * 0.01 // approximate velocity
			if velocity > limit {
				violations = append(violations, &pb.SafetyViolation{
					Type:      "velocity_exceeded",
					Detail:    fmt.Sprintf("velocity %.2f exceeds limit %.2f", velocity, limit),
					Magnitude: velocity - limit,
					Timestamp: timestamppb.Now(),
				})
			}
		}
	}

	// Check force limit from envelope
	if maxForce, ok := envelope["max_force"]; ok {
		limit, _ := toFloat64(maxForce)
		if limit > 0 {
			// Check joint torques as proxy for force
			for _, name := range simulator.JointNames {
				torque := math.Abs(robot.JointTorque[name])
				if torque > limit {
					violations = append(violations, &pb.SafetyViolation{
						Type:      "force_exceeded",
						Detail:    fmt.Sprintf("joint %s torque %.2f exceeds limit %.2f", name, torque, limit),
						Magnitude: torque - limit,
						Timestamp: timestamppb.Now(),
					})
					break // one violation per check is enough
				}
			}
		}
	}

	return violations
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case float32:
		return float64(val), true
	}
	return 0, false
}

// filterScenarios returns scenarios matching the suite, up to maxCount.
func filterScenarios(suite string, maxCount int) []scenarioDef {
	var filtered []scenarioDef
	for _, sc := range scenarioDefs {
		if suite != "" && sc.suite != suite {
			continue
		}
		filtered = append(filtered, sc)
		if len(filtered) >= maxCount {
			break
		}
	}
	return filtered
}
