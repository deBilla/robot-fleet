package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/dimuthu/robot-fleet/internal/store"
)

// robotService implements RobotService with injected dependencies.
type robotService struct {
	repo              store.RobotRepository
	cache             store.CacheStore
	cmdReg            *command.Registry
	inferenceEndpoint string
	inferenceTimeout  time.Duration
}

// NewRobotService creates a new RobotService with the given dependencies.
func NewRobotService(
	repo store.RobotRepository,
	cache store.CacheStore,
	cmdReg *command.Registry,
	inferenceEndpoint string,
	inferenceTimeout time.Duration,
) RobotService {
	return &robotService{
		repo:              repo,
		cache:             cache,
		cmdReg:            cmdReg,
		inferenceEndpoint: inferenceEndpoint,
		inferenceTimeout:  inferenceTimeout,
	}
}

func (s *robotService) ListRobots(ctx context.Context, tenantID string, limit, offset int) (*ListRobotsResult, error) {
	robots, total, err := s.repo.ListRobots(ctx, tenantID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list robots: %w", err)
	}
	return &ListRobotsResult{Robots: robots, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *robotService) GetRobot(ctx context.Context, robotID string) (*RobotResult, error) {
	hotState, err := s.cache.GetRobotState(ctx, robotID)
	if err == nil {
		return &RobotResult{HotState: hotState}, nil
	}
	record, err := s.repo.GetRobot(ctx, robotID)
	if err != nil {
		return nil, fmt.Errorf("get robot %s: %w", robotID, err)
	}
	return &RobotResult{Record: record}, nil
}

func (s *robotService) GetTelemetry(ctx context.Context, robotID string) (*TelemetryResult, error) {
	state, err := s.cache.GetRobotState(ctx, robotID)
	if err != nil {
		return nil, fmt.Errorf("get telemetry for %s: %w", robotID, err)
	}
	return &TelemetryResult{RobotID: robotID, State: state, Timestamp: time.Now().UTC()}, nil
}

func (s *robotService) GetFleetMetrics(ctx context.Context, tenantID string) (*FleetMetrics, error) {
	const maxFleetRobots = 1000
	robots, total, err := s.repo.ListRobots(ctx, tenantID, maxFleetRobots, 0)
	if err != nil {
		return nil, fmt.Errorf("get fleet metrics: %w", err)
	}

	var active, idle, errored int
	var totalBattery float64
	for _, robot := range robots {
		switch robot.Status {
		case "active":
			active++
		case "idle", "charging":
			idle++
		default:
			errored++
		}
		totalBattery += robot.BatteryLevel
	}

	avgBattery := 0.0
	if total > 0 {
		avgBattery = totalBattery / float64(total)
	}

	middleware.RobotsTotal.Set(float64(total))
	middleware.RobotsActive.Set(float64(active))
	middleware.RobotsError.Set(float64(errored))
	middleware.AvgBatteryLevel.Set(avgBattery)

	return &FleetMetrics{
		TotalRobots:  total,
		ActiveRobots: active,
		IdleRobots:   idle,
		ErrorRobots:  errored,
		AvgBattery:   avgBattery,
		Timestamp:    time.Now().UTC(),
	}, nil
}

func (s *robotService) GetUsage(ctx context.Context, tenantID string) (*UsageResult, error) {
	date := time.Now().Format("2006-01-02")
	apiCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "api_calls", date)
	inferenceCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "inference_calls", date)

	return &UsageResult{
		TenantID:       tenantID,
		Date:           date,
		APICalls:       apiCalls,
		InferenceCalls: inferenceCalls,
	}, nil
}
