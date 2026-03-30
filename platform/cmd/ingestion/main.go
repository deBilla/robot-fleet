package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/ingestion"
	"github.com/dimuthu/robot-fleet/internal/middleware"
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

	// Kafka command dispatch pipeline
	cmdConsumer := ingestion.NewKafkaConsumer(cfg.KafkaBrokers, cfg.KafkaCommandDispatchTopic, "fleetos-cmd-dispatch")
	defer cmdConsumer.Close()
	cmdDispatcher := ingestion.NewCommandDispatcher(cmdConsumer)
	go cmdDispatcher.Start(ctx)

	// Kafka producer for command acks (ingestion → Temporal bridge)
	ackProducer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaCommandAcksTopic)
	if err != nil {
		slog.Warn("kafka ack producer not available, command acks will use Redis", "error", err)
	} else {
		defer ackProducer.Close()
	}

	// Create gRPC server with telemetry handler
	server := grpc.NewServer()
	handler := ingestion.NewTelemetryHandler(producer, redisClient)
	handler.SetCommandPipeline(cmdDispatcher, ackProducer)
	pb.RegisterTelemetryServiceServer(server, handler)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		slog.Error("failed to listen", "port", cfg.GRPCPort, "error", err)
		os.Exit(1)
	}

	// Expose Prometheus metrics on HTTP :9090
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", middleware.MetricsHandler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	metricsServer := &http.Server{Addr: ":9090", Handler: metricsMux}
	go func() {
		slog.Info("metrics server starting", "addr", ":9090")
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics server error", "error", err)
		}
	}()

	slog.Info("ingestion service starting",
		"grpc_port", cfg.GRPCPort,
		"kafka_brokers", cfg.KafkaBrokers,
		"topic", cfg.KafkaTelemetryTopic,
	)

	go func() {
		<-ctx.Done()
		slog.Info("graceful shutdown")
		metricsServer.Shutdown(ctx)
		server.GracefulStop()
	}()

	if err := server.Serve(lis); err != nil {
		slog.Error("grpc server error", "error", err)
	}
}
