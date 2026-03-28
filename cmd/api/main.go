package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dimuthu/robot-fleet/internal/api"
	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize OpenTelemetry tracing
	shutdownTracer, err := middleware.InitTracer(ctx, "fleetos-api", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		slog.Error("failed to init tracer", "error", err)
	} else {
		defer shutdownTracer(ctx)
	}

	// Initialize stores
	pg, err := store.NewPostgresStore(ctx, cfg.PostgresDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer pg.Close()

	redis, err := store.NewRedisStore(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		slog.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// Auth
	tokenSvc := auth.NewTokenService(cfg.JWTSecret, cfg.OAuth2Issuer)
	apiKeys := auth.NewAPIKeyStore()

	// ClickHouse (analytics OLAP — optional, graceful if unavailable)
	chAddr := os.Getenv("CLICKHOUSE_ADDR")
	if chAddr == "" {
		chAddr = "clickhouse:9000"
	}
	var analyticsSvc service.AnalyticsService
	ch, err := store.NewClickHouseStore(ctx, chAddr)
	if err != nil {
		slog.Warn("clickhouse not available, analytics endpoints disabled", "error", err)
	} else {
		defer ch.Close()
		analyticsSvc = service.NewAnalyticsService(ch, redis)
	}

	// Service layer
	cmdReg := command.DefaultRegistry()
	robotSvc := service.NewRobotService(pg, redis, cmdReg, cfg.InferenceEndpoint, cfg.InferenceTimeout)
	modelSvc := service.NewModelRegistryService(pg)

	// HTTP handlers (thin adapters)
	handler := api.NewHandler(robotSvc, redis, apiKeys)
	modelHandler := api.NewModelHandler(modelSvc)
	var analyticsHandler *api.AnalyticsHandler
	if analyticsSvc != nil {
		analyticsHandler = api.NewAnalyticsHandler(analyticsSvc)
	}

	// Build router
	mux := http.NewServeMux()

	// Health check (no auth)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// API routes (authenticated)
	authedMux := http.NewServeMux()
	authedMux.HandleFunc("GET /api/v1/robots", handler.ListRobots)
	authedMux.HandleFunc("GET /api/v1/robots/{id}", handler.GetRobot)
	authedMux.HandleFunc("POST /api/v1/robots/{id}/command", handler.SendCommand)
	authedMux.HandleFunc("GET /api/v1/robots/{id}/telemetry", handler.GetTelemetry)
	authedMux.HandleFunc("POST /api/v1/inference", handler.RunInference)
	authedMux.HandleFunc("POST /api/v1/robots/{id}/semantic-command", handler.SemanticCommand)
	authedMux.HandleFunc("GET /api/v1/fleet/metrics", handler.GetFleetMetrics)
	authedMux.HandleFunc("GET /api/v1/usage", handler.GetUsage)
	// Model registry routes
	authedMux.HandleFunc("POST /api/v1/models", modelHandler.RegisterModel)
	authedMux.HandleFunc("GET /api/v1/models", modelHandler.ListModels)
	authedMux.HandleFunc("GET /api/v1/models/{id}", modelHandler.GetModel)
	authedMux.HandleFunc("POST /api/v1/models/{id}/deploy", modelHandler.DeployModel)
	authedMux.HandleFunc("POST /api/v1/models/{id}/archive", modelHandler.ArchiveModel)
	// Analytics routes (only if ClickHouse is available)
	if analyticsHandler != nil {
		authedMux.HandleFunc("GET /api/v1/analytics/fleet", analyticsHandler.GetFleetAnalytics)
		authedMux.HandleFunc("GET /api/v1/analytics/robots/{id}", analyticsHandler.GetRobotAnalytics)
		authedMux.HandleFunc("GET /api/v1/analytics/anomalies", analyticsHandler.GetAnomalies)
	}
	// Stack middleware: CORS → Logging → Auth → RateLimit → Usage → Handler
	authed := auth.AuthMiddleware(tokenSvc, apiKeys)(
		middleware.RateLimiter(redis, cfg.RateLimitRPS, cfg.RateLimitBurst)(
			middleware.UsageMetering(redis)(authedMux),
		),
	)

	mux.Handle("/api/", authed)

	// WebSocket endpoint (no auth — browser WebSocket can't set headers)
	mux.HandleFunc("GET /api/v1/ws/telemetry", handler.WebSocketTelemetry)

	// Prometheus metrics endpoint
	mux.Handle("GET /metrics", middleware.MetricsHandler())

	// Wrap with tracing → metrics → logging → CORS
	finalHandler := middleware.CORS(middleware.Metrics(middleware.Logging(middleware.Tracing(mux))))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      finalHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down api server")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown error", "error", err)
		}
	}()

	slog.Info("api server starting", "port", cfg.HTTPPort)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
