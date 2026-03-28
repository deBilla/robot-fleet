package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/simulator"
)

func main() {
	robotCount := flag.Int("robots", 0, "number of simulated robots (overrides SIM_ROBOT_COUNT)")
	target := flag.String("target", "", "gRPC target address (overrides GRPC ingestion endpoint)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	if *robotCount > 0 {
		cfg.SimRobotCount = *robotCount
	}
	grpcTarget := fmt.Sprintf("localhost:%d", cfg.GRPCPort)
	if *target != "" {
		grpcTarget = *target
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down simulator")
		cancel()
	}()

	fleet := simulator.NewFleet(cfg.SimRobotCount, cfg.SimTickInterval, grpcTarget, cfg.RedisAddr)

	slog.Info("starting robot fleet simulator",
		"robots", cfg.SimRobotCount,
		"target", grpcTarget,
		"redis", cfg.RedisAddr,
		"tick_interval", cfg.SimTickInterval,
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		fleet.Run(ctx)
	}()

	wg.Wait()
	slog.Info("simulator stopped")
}
