package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg := Load()

	if cfg.ServiceName != "fleetos" {
		t.Errorf("expected fleetos, got %s", cfg.ServiceName)
	}
	if cfg.Environment != "dev" {
		t.Errorf("expected dev, got %s", cfg.Environment)
	}
	if cfg.GRPCPort != 50051 {
		t.Errorf("expected 50051, got %d", cfg.GRPCPort)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("expected 8080, got %d", cfg.HTTPPort)
	}
	if len(cfg.KafkaBrokers) != 1 || cfg.KafkaBrokers[0] != "localhost:9092" {
		t.Errorf("unexpected kafka brokers: %v", cfg.KafkaBrokers)
	}
	if cfg.KafkaTelemetryTopic != "robot.telemetry" {
		t.Errorf("expected robot.telemetry, got %s", cfg.KafkaTelemetryTopic)
	}
	if cfg.RedisAddr != "localhost:6379" {
		t.Errorf("expected localhost:6379, got %s", cfg.RedisAddr)
	}
	if cfg.SimRobotCount != 10 {
		t.Errorf("expected 10, got %d", cfg.SimRobotCount)
	}
	if cfg.SimTickInterval != 100*time.Millisecond {
		t.Errorf("expected 100ms, got %v", cfg.SimTickInterval)
	}
	if cfg.RateLimitRPS != 100 {
		t.Errorf("expected 100, got %d", cfg.RateLimitRPS)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("SERVICE_NAME", "custom-svc")
	t.Setenv("HTTP_PORT", "9999")
	t.Setenv("SIM_ROBOT_COUNT", "50")
	t.Setenv("RATE_LIMIT_RPS", "500")

	cfg := Load()

	if cfg.ServiceName != "custom-svc" {
		t.Errorf("expected custom-svc, got %s", cfg.ServiceName)
	}
	if cfg.HTTPPort != 9999 {
		t.Errorf("expected 9999, got %d", cfg.HTTPPort)
	}
	if cfg.SimRobotCount != 50 {
		t.Errorf("expected 50, got %d", cfg.SimRobotCount)
	}
	if cfg.RateLimitRPS != 500 {
		t.Errorf("expected 500, got %d", cfg.RateLimitRPS)
	}
}

func TestLoad_InvalidIntFallback(t *testing.T) {
	t.Setenv("HTTP_PORT", "not-a-number")

	cfg := Load()

	if cfg.HTTPPort != 8080 {
		t.Errorf("expected fallback 8080, got %d", cfg.HTTPPort)
	}
}

func TestGetEnv(t *testing.T) {
	t.Setenv("TEST_KEY", "test_value")

	if v := getEnv("TEST_KEY", "default"); v != "test_value" {
		t.Errorf("expected test_value, got %s", v)
	}
	if v := getEnv("NONEXISTENT_KEY", "default"); v != "default" {
		t.Errorf("expected default, got %s", v)
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("INT_KEY", "42")

	if v := getEnvInt("INT_KEY", 0); v != 42 {
		t.Errorf("expected 42, got %d", v)
	}
	if v := getEnvInt("NONEXISTENT_INT", 99); v != 99 {
		t.Errorf("expected 99, got %d", v)
	}
}
