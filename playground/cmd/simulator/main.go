package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/dimuthu/robot-fleet-playground/internal/config"
	pb "github.com/dimuthu/robot-fleet-playground/internal/simulation"
	"github.com/dimuthu/robot-fleet-playground/internal/simulator"
	"github.com/dimuthu/robot-fleet-playground/internal/validation"
	"google.golang.org/grpc"
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

	var wg sync.WaitGroup

	// Start SimulationService gRPC server (Uranus-style validation)
	simServer := grpc.NewServer()
	pb.RegisterSimulationServiceServer(simServer, validation.NewServer())

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.SimulationPort))
	if err != nil {
		slog.Error("failed to listen for simulation service", "port", cfg.SimulationPort, "error", err)
		os.Exit(1)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("simulation service started", "port", cfg.SimulationPort)
		if err := simServer.Serve(lis); err != nil {
			slog.Error("simulation server error", "error", err)
		}
	}()

	// Graceful stop for gRPC server on context cancel
	go func() {
		<-ctx.Done()
		simServer.GracefulStop()
	}()

	// Start fleet telemetry streaming
	fleet := simulator.NewFleet(cfg.SimRobotCount, cfg.SimTickInterval, grpcTarget, cfg.RedisAddr)

	slog.Info("starting robot fleet simulator",
		"robots", cfg.SimRobotCount,
		"target", grpcTarget,
		"redis", cfg.RedisAddr,
		"tick_interval", cfg.SimTickInterval,
		"simulation_port", cfg.SimulationPort,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		fleet.Run(ctx)
	}()

	wg.Wait()
	slog.Info("simulator stopped")
}
