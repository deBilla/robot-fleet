package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/ingestion"
	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down ingestion service")
		cancel()
	}()

	// Initialize Kafka producer
	producer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaTelemetryTopic)
	if err != nil {
		slog.Error("failed to create kafka producer", "error", err)
		os.Exit(1)
	}
	defer producer.Close()

	// Connect to Redis for command pub/sub bridge
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisClient.Close()

	// Create gRPC server with telemetry handler
	server := grpc.NewServer()
	handler := ingestion.NewTelemetryHandler(producer, redisClient)
	pb.RegisterTelemetryServiceServer(server, handler)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		slog.Error("failed to listen", "port", cfg.GRPCPort, "error", err)
		os.Exit(1)
	}

	slog.Info("ingestion service starting",
		"grpc_port", cfg.GRPCPort,
		"kafka_brokers", cfg.KafkaBrokers,
		"topic", cfg.KafkaTelemetryTopic,
	)

	go func() {
		<-ctx.Done()
		slog.Info("graceful shutdown")
		server.GracefulStop()
	}()

	if err := server.Serve(lis); err != nil {
		slog.Error("grpc server error", "error", err)
	}
}
