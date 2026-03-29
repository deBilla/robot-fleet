package service

import (
	"context"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/store"
	"github.com/redis/go-redis/v9"
)

// --- Mocks ---

type mockRepo struct {
	robots []*store.RobotRecord
	err    error
}

func (m *mockRepo) UpsertRobot(_ context.Context, _ *store.RobotRecord) error { return m.err }
func (m *mockRepo) GetRobot(_ context.Context, id string) (*store.RobotRecord, error) {
	for _, r := range m.robots {
		if r.ID == id {
			return r, nil
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return nil, ErrNotFound
}
func (m *mockRepo) ListRobots(_ context.Context, _ string, limit, offset int) ([]*store.RobotRecord, int, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	end := offset + limit
	if end > len(m.robots) {
		end = len(m.robots)
	}
	if offset > len(m.robots) {
		return nil, len(m.robots), nil
	}
	return m.robots[offset:end], len(m.robots), nil
}
func (m *mockRepo) StoreTelemetryEvent(_ context.Context, _, _ string, _ []byte, _ time.Time) error {
	return m.err
}
func (m *mockRepo) StoreAPIUsage(_ context.Context, _, _, _ string, _ int, _ int64) error {
	return m.err
}
func (m *mockRepo) InsertCommandAudit(_ context.Context, _ *store.CommandAuditEntry) error {
	return m.err
}
func (m *mockRepo) ListCommandAudit(_ context.Context, _, _ string, _ int) ([]*store.CommandAuditEntry, error) {
	return nil, m.err
}
func (m *mockRepo) UpdateCommandAuditStatus(_ context.Context, _, _ string) error {
	return m.err
}
func (m *mockRepo) Close() {}

type mockCache struct {
	states   map[string]*store.RobotHotState
	counters map[string]int64
	published []string
	err      error
}

func newMockCache() *mockCache {
	return &mockCache{
		states:   make(map[string]*store.RobotHotState),
		counters: make(map[string]int64),
	}
}

func (m *mockCache) SetRobotState(_ context.Context, s *store.RobotHotState) error {
	m.states[s.RobotID] = s
	return m.err
}
func (m *mockCache) GetRobotState(_ context.Context, id string) (*store.RobotHotState, error) {
	if s, ok := m.states[id]; ok {
		return s, nil
	}
	return nil, ErrNotFound
}
func (m *mockCache) CheckRateLimit(_ context.Context, _ string, limit int, _ time.Duration) (bool, int, time.Time, error) {
	return true, limit, time.Now(), nil
}
func (m *mockCache) IncrementUsageCounter(_ context.Context, tenant, metric string) (int64, error) {
	key := tenant + ":" + metric
	m.counters[key]++
	return m.counters[key], nil
}
func (m *mockCache) GetUsageCounter(_ context.Context, tenant, metric, date string) (int64, error) {
	key := tenant + ":" + metric
	return m.counters[key], nil
}
func (m *mockCache) PublishEvent(_ context.Context, channel string, _ []byte) error {
	m.published = append(m.published, channel)
	return m.err
}
func (m *mockCache) Subscribe(_ context.Context, _ ...string) *redis.PubSub { return nil }
func (m *mockCache) SetCacheJSON(_ context.Context, key string, data []byte, _ time.Duration) error {
	return nil
}
func (m *mockCache) GetCacheJSON(_ context.Context, key string) ([]byte, error) {
	return nil, ErrNotFound
}
func (m *mockCache) CheckCommandDedup(_ context.Context, _ string) (int64, error) { return 0, nil }
func (m *mockCache) SetCommandDedup(_ context.Context, _ string, _ int64) error   { return nil }
func (m *mockCache) Close() {}

// --- Robot Service Tests ---

func TestListRobots(t *testing.T) {
	repo := &mockRepo{robots: []*store.RobotRecord{
		{ID: "r1", Name: "r1", Status: "active", TenantID: "t1"},
		{ID: "r2", Name: "r2", Status: "idle", TenantID: "t1"},
		{ID: "r3", Name: "r3", Status: "active", TenantID: "t1"},
	}}
	svc := NewRobotService(repo, newMockCache(), command.DefaultRegistry(), "", 0)

	result, err := svc.ListRobots(context.Background(), "t1", 2, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("expected total=3, got %d", result.Total)
	}
	if len(result.Robots) != 2 {
		t.Errorf("expected 2 robots, got %d", len(result.Robots))
	}
	if result.Limit != 2 || result.Offset != 0 {
		t.Errorf("expected limit=2 offset=0, got limit=%d offset=%d", result.Limit, result.Offset)
	}
}

func TestGetRobot_HotState(t *testing.T) {
	cache := newMockCache()
	cache.states["r1"] = &store.RobotHotState{RobotID: "r1", Status: "active", BatteryLevel: 0.9}

	svc := NewRobotService(&mockRepo{}, cache, command.DefaultRegistry(), "", 0)

	result, err := svc.GetRobot(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HotState == nil {
		t.Fatal("expected HotState to be set")
	}
	if result.HotState.BatteryLevel != 0.9 {
		t.Errorf("expected battery=0.9, got %f", result.HotState.BatteryLevel)
	}
	if result.Record != nil {
		t.Error("expected Record to be nil when HotState exists")
	}
}

func TestGetRobot_FallbackToPostgres(t *testing.T) {
	repo := &mockRepo{robots: []*store.RobotRecord{
		{ID: "r1", Name: "r1", Status: "idle"},
	}}
	svc := NewRobotService(repo, newMockCache(), command.DefaultRegistry(), "", 0)

	result, err := svc.GetRobot(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Record == nil {
		t.Fatal("expected Record from postgres fallback")
	}
	if result.Record.Status != "idle" {
		t.Errorf("expected idle, got %s", result.Record.Status)
	}
	if result.HotState != nil {
		t.Error("expected HotState to be nil on fallback")
	}
}

func TestGetRobot_NotFound(t *testing.T) {
	svc := NewRobotService(&mockRepo{}, newMockCache(), command.DefaultRegistry(), "", 0)

	_, err := svc.GetRobot(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent robot")
	}
}

func TestGetTelemetry(t *testing.T) {
	cache := newMockCache()
	cache.states["r1"] = &store.RobotHotState{RobotID: "r1", Status: "active"}

	svc := NewRobotService(&mockRepo{}, cache, command.DefaultRegistry(), "", 0)

	result, err := svc.GetTelemetry(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RobotID != "r1" {
		t.Errorf("expected r1, got %s", result.RobotID)
	}
}

func TestGetFleetMetrics(t *testing.T) {
	repo := &mockRepo{robots: []*store.RobotRecord{
		{ID: "r1", Status: "active", BatteryLevel: 0.8},
		{ID: "r2", Status: "active", BatteryLevel: 0.6},
		{ID: "r3", Status: "charging", BatteryLevel: 0.3},
		{ID: "r4", Status: "error", BatteryLevel: 0.1},
	}}
	svc := NewRobotService(repo, newMockCache(), command.DefaultRegistry(), "", 0)

	metrics, err := svc.GetFleetMetrics(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics.TotalRobots != 4 {
		t.Errorf("expected 4 total, got %d", metrics.TotalRobots)
	}
	if metrics.ActiveRobots != 2 {
		t.Errorf("expected 2 active, got %d", metrics.ActiveRobots)
	}
	if metrics.IdleRobots != 1 {
		t.Errorf("expected 1 idle, got %d", metrics.IdleRobots)
	}
	if metrics.ErrorRobots != 1 {
		t.Errorf("expected 1 error, got %d", metrics.ErrorRobots)
	}
}

// --- Command Service Tests ---

func TestSendCommand(t *testing.T) {
	cache := newMockCache()
	svc := NewRobotService(&mockRepo{}, cache, command.DefaultRegistry(), "", 0)

	result, err := svc.SendCommand(context.Background(), "r1", "dance", map[string]any{}, "tenant-dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "queued" {
		t.Errorf("expected queued, got %s", result.Status)
	}
	if result.RobotID != "r1" {
		t.Errorf("expected r1, got %s", result.RobotID)
	}
	if len(cache.published) != 1 || cache.published[0] != "commands:r1" {
		t.Errorf("expected publish to commands:r1, got %v", cache.published)
	}
}

func TestSemanticCommand(t *testing.T) {
	cache := newMockCache()
	svc := NewRobotService(&mockRepo{}, cache, command.DefaultRegistry(), "", 0)

	result, err := svc.SemanticCommand(context.Background(), "r1", "wave hello", "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interpreted.Type != "wave" {
		t.Errorf("expected wave, got %s", result.Interpreted.Type)
	}
	if result.Original != "wave hello" {
		t.Errorf("expected original preserved, got %s", result.Original)
	}
	if result.Status != "queued" {
		t.Errorf("expected queued, got %s", result.Status)
	}
	// Verify usage was tracked
	if cache.counters["t1:semantic_commands"] != 1 {
		t.Errorf("expected 1 semantic_commands counter, got %d", cache.counters["t1:semantic_commands"])
	}
}

// --- Usage Tests ---

func TestGetUsage(t *testing.T) {
	cache := newMockCache()
	cache.counters["t1:api_calls"] = 42
	cache.counters["t1:inference_calls"] = 7

	svc := NewRobotService(&mockRepo{}, cache, command.DefaultRegistry(), "", 0)

	result, err := svc.GetUsage(context.Background(), "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.APICalls != 42 {
		t.Errorf("expected 42 api calls, got %d", result.APICalls)
	}
	if result.InferenceCalls != 7 {
		t.Errorf("expected 7 inference calls, got %d", result.InferenceCalls)
	}
}
