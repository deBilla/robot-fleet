package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds configuration for the playground simulator.
type Config struct {
	GRPCPort          int
	SimulationPort    int
	RedisAddr         string
	SimRobotCount     int
	SimTickInterval   time.Duration
}

func Load() *Config {
	return &Config{
		GRPCPort:        envInt("GRPC_PORT", 50051),
		SimulationPort:  envInt("SIMULATION_GRPC_PORT", 50052),
		RedisAddr:       envStr("REDIS_ADDR", "localhost:6379"),
		SimRobotCount:   envInt("SIM_ROBOT_COUNT", 10),
		SimTickInterval: time.Duration(envInt("SIM_TICK_MS", 100)) * time.Millisecond,
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
