package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/api"
	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"context"
	"errors"
)

// --- Mock stores for HTTP integration tests ---

type testRepo struct {
	robots []*store.RobotRecord
}

func (r *testRepo) UpsertRobot(_ context.Context, rec *store.RobotRecord) error { return nil }
func (r *testRepo) GetRobot(_ context.Context, id string) (*store.RobotRecord, error) {
	for _, robot := range r.robots {
		if robot.ID == id {
			return robot, nil
		}
	}
	return nil, errors.New("not found")
}
func (r *testRepo) ListRobots(_ context.Context, _ string, limit, offset int) ([]*store.RobotRecord, int, error) {
	end := offset + limit
	if end > len(r.robots) {
		end = len(r.robots)
	}
	return r.robots[offset:end], len(r.robots), nil
}
func (r *testRepo) StoreTelemetryEvent(_ context.Context, _, _ string, _ []byte, _ time.Time) error {
	return nil
}
func (r *testRepo) StoreAPIUsage(_ context.Context, _, _, _ string, _ int, _ int64) error {
	return nil
}
func (r *testRepo) InsertCommandAudit(_ context.Context, _ *store.CommandAuditEntry) error {
	return nil
}
func (r *testRepo) ListCommandAudit(_ context.Context, _, _ string, _ int) ([]*store.CommandAuditEntry, error) {
	return nil, nil
}
func (r *testRepo) UpdateCommandAuditStatus(_ context.Context, _, _ string) error {
	return nil
}
func (r *testRepo) ListAllActiveRobots(_ context.Context, since time.Time, limit int) ([]*store.RobotRecord, error) {
	var result []*store.RobotRecord
	for _, robot := range r.robots {
		if !robot.LastSeen.Before(since) {
			result = append(result, robot)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
func (r *testRepo) UpdateRobotInferenceModel(_ context.Context, _, _ string) error { return nil }
func (r *testRepo) ListRobotsByInferenceModel(_ context.Context, _ string) ([]*store.RobotRecord, error) {
	return nil, nil
}
func (r *testRepo) Close() {}

type testCache struct {
	states   map[string]*store.RobotHotState
	counters map[string]int64
	pubCh    chan string
}

func newTestCache() *testCache {
	return &testCache{
		states:   make(map[string]*store.RobotHotState),
		counters: make(map[string]int64),
		pubCh:    make(chan string, 100),
	}
}
func (c *testCache) SetRobotState(_ context.Context, s *store.RobotHotState) error {
	c.states[s.RobotID] = s
	return nil
}
func (c *testCache) GetRobotState(_ context.Context, id string) (*store.RobotHotState, error) {
	if s, ok := c.states[id]; ok {
		return s, nil
	}
	return nil, errors.New("not found")
}
func (c *testCache) CheckRateLimit(_ context.Context, _ string, limit int, _ time.Duration) (bool, int, time.Time, error) {
	return true, limit, time.Now(), nil
}
func (c *testCache) IncrementUsageCounter(_ context.Context, tenant, metric string) (int64, error) {
	c.counters[tenant+":"+metric]++
	return c.counters[tenant+":"+metric], nil
}
func (c *testCache) GetUsageCounter(_ context.Context, tenant, metric, _ string) (int64, error) {
	return c.counters[tenant+":"+metric], nil
}
func (c *testCache) PublishEvent(_ context.Context, channel string, data []byte) error {
	c.pubCh <- string(data)
	return nil
}
func (c *testCache) Subscribe(_ context.Context, _ ...string) *redis.PubSub { return nil }
func (c *testCache) SetCacheJSON(_ context.Context, _ string, _ []byte, _ time.Duration) error { return nil }
func (c *testCache) GetCacheJSON(_ context.Context, _ string) ([]byte, error) { return nil, errors.New("miss") }
func (c *testCache) CheckCommandDedup(_ context.Context, _ string) (int64, error) { return 0, nil }
func (c *testCache) SetCommandDedup(_ context.Context, _ string, _ int64) error   { return nil }
func (c *testCache) Close()                                                       {}

func setupTestServer(t *testing.T) (*httptest.Server, *testCache) {
	t.Helper()

	now := time.Now()
	repo := &testRepo{robots: []*store.RobotRecord{
		{ID: "robot-0001", Name: "robot-0001", Model: "humanoid-v1", Status: "active", BatteryLevel: 0.85, TenantID: "tenant-dev", LastSeen: now},
		{ID: "robot-0002", Name: "robot-0002", Model: "humanoid-v1", Status: "charging", BatteryLevel: 0.2, TenantID: "tenant-dev", LastSeen: now},
	}}
	cache := newTestCache()
	cache.states["robot-0001"] = &store.RobotHotState{RobotID: "robot-0001", Status: "active", BatteryLevel: 0.85}

	cmdReg := command.DefaultRegistry()
	svc := service.NewRobotService(repo, cache, cmdReg, "", 0)
	apiKeys := auth.NewAPIKeyStore()
	handler := api.NewHandler(svc, cache, apiKeys)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/robots", handler.ListRobots)
	mux.HandleFunc("GET /api/v1/robots/{id}", handler.GetRobot)
	mux.HandleFunc("POST /api/v1/robots/{id}/command", handler.SendCommand)
	mux.HandleFunc("POST /api/v1/robots/{id}/semantic-command", handler.SemanticCommand)
	mux.HandleFunc("GET /api/v1/fleet/metrics", handler.GetFleetMetrics)

	// Wrap with auth middleware
	authedHandler := auth.AuthMiddleware(auth.NewTokenService("test-secret", ""), apiKeys)(mux)
	server := httptest.NewServer(authedHandler)
	return server, cache
}

func TestHTTP_ListRobots(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/robots", nil)
	req.Header.Set("X-API-Key", "dev-key-001")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 robots, got %d", total)
	}
}

func TestHTTP_GetRobot_HotState(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/robots/robot-0001", nil)
	req.Header.Set("X-API-Key", "dev-key-001")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["robot_id"] != "robot-0001" {
		t.Errorf("expected robot-0001, got %v", body["robot_id"])
	}
}

func TestHTTP_SendCommand(t *testing.T) {
	server, cache := setupTestServer(t)
	defer server.Close()

	body := `{"type":"dance","params":{}}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/robots/robot-0001/command", strings.NewReader(body))
	req.Header.Set("X-API-Key", "dev-key-001")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	// Verify command was published
	select {
	case msg := <-cache.pubCh:
		if !strings.Contains(msg, "dance") {
			t.Errorf("expected dance command in published message, got: %s", msg)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for published command")
	}
}

func TestHTTP_SemanticCommand(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	body := `{"instruction":"wave hello","robot_id":"robot-0001"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/robots/robot-0001/semantic-command", strings.NewReader(body))
	req.Header.Set("X-API-Key", "dev-key-001")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	interpreted := result["interpreted"].(map[string]any)
	if interpreted["type"] != "wave" {
		t.Errorf("expected 'wave' command, got %v", interpreted["type"])
	}
}

func TestHTTP_FleetMetrics(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/fleet/metrics", nil)
	req.Header.Set("X-API-Key", "dev-key-001")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var metrics map[string]any
	json.NewDecoder(resp.Body).Decode(&metrics)
	if int(metrics["total_robots"].(float64)) != 2 {
		t.Errorf("expected 2 total robots, got %v", metrics["total_robots"])
	}
	if int(metrics["active_robots"].(float64)) != 1 {
		t.Errorf("expected 1 active robot, got %v", metrics["active_robots"])
	}
}

func TestHTTP_Unauthorized(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/robots", nil)
	// No API key

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHTTP_InvalidAPIKey(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/robots", nil)
	req.Header.Set("X-API-Key", "invalid-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHTTP_InferenceEndpoint_MockServer(t *testing.T) {
	// Create a mock inference backend
	inferenceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"predicted_actions": []map[string]any{
				{"joint": "left_shoulder_pitch", "position": 0.5, "velocity": 0.1, "torque": 2.0},
			},
			"confidence":      0.87,
			"model_id":        "groot-n1-v1.5",
			"model_version":   "v1.5.0",
			"embodiment":      "humanoid-v1",
			"action_horizon":  16,
			"action_dim":      20,
			"diffusion_steps": 10,
			"latency_ms":      42,
		})
	}))
	defer inferenceServer.Close()

	// Setup API with inference endpoint pointing to mock
	repo := &testRepo{robots: nil}
	cache := newTestCache()
	cmdReg := command.DefaultRegistry()
	// Strip http:// and use as endpoint
	endpoint := strings.TrimPrefix(inferenceServer.URL, "http://")
	svc := service.NewRobotService(repo, cache, cmdReg, endpoint, 5*time.Second)
	apiKeys := auth.NewAPIKeyStore()
	handler := api.NewHandler(svc, cache, apiKeys)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/inference", handler.RunInference)
	authed := auth.AuthMiddleware(auth.NewTokenService("test-secret", ""), apiKeys)(mux)
	server := httptest.NewServer(authed)
	defer server.Close()

	body := `{"instruction":"wave hello","model_id":"groot-n1-v1.5"}`
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/inference", strings.NewReader(body))
	req.Header.Set("X-API-Key", "dev-key-001")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["confidence"].(float64) != 0.87 {
		t.Errorf("expected confidence=0.87, got %v", result["confidence"])
	}
	actions := result["predicted_actions"].([]any)
	if len(actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(actions))
	}
}

func TestWebSocket_AuthRequired(t *testing.T) {
	server, _ := setupTestServer(t)
	defer server.Close()

	// Try WebSocket without api_key — should be rejected
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws/telemetry"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected WebSocket dial to fail without api_key")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}
