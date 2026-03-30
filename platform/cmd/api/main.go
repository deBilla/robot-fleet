package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dimuthu/robot-fleet/internal/api"
	"github.com/dimuthu/robot-fleet/internal/auth"
	"github.com/dimuthu/robot-fleet/internal/command"
	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/ingestion"
	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
	temporalpkg "github.com/dimuthu/robot-fleet/internal/temporal"
	"go.temporal.io/sdk/client"
)

// spaHandler wraps a file server to serve index.html for any path that doesn't
// match a static file, enabling client-side routing.
func spaHandler(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if f, err := fsys.Open(path); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routes
		if !strings.Contains(path, ".") {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

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

	// Wire DB-backed API key lookup (hashed keys in api_keys table)
	apiKeys.SetDBLookup(func(ctx context.Context, keyHash string) (*auth.APIKeyInfo, error) {
		rec, err := pg.GetAPIKey(ctx, keyHash)
		if err != nil {
			return nil, err
		}
		tier := "free"
		if tenant, err := pg.GetTenant(ctx, rec.TenantID); err == nil {
			tier = tenant.Tier
		}
		return &auth.APIKeyInfo{TenantID: rec.TenantID, Role: rec.Role, Tier: tier}, nil
	})

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

	// Temporal client (control plane — optional, graceful fallback to legacy dispatch)
	var temporalClient client.Client
	if cfg.TemporalEnabled {
		tc, err := temporalpkg.NewClient(cfg.TemporalHostPort, cfg.TemporalNamespace)
		if err != nil {
			slog.Warn("temporal not available, falling back to direct dispatch", "error", err)
		} else {
			temporalClient = tc
			defer tc.Close()
			slog.Info("temporal connected", "host", cfg.TemporalHostPort, "namespace", cfg.TemporalNamespace)
		}
	}

	// Kafka command producer (optional — graceful if Kafka unavailable)
	cmdProducer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaCommandTopic)
	if err != nil {
		slog.Warn("kafka command producer not available, commands will use fallback path", "error", err)
	} else {
		defer cmdProducer.Close()
	}

	// Service layer
	cmdReg := command.DefaultRegistry()
	robotOpts := []service.RobotServiceOption{}
	if cmdProducer != nil {
		robotOpts = append(robotOpts, service.WithCommandProducer(cmdProducer))
	}
	if temporalClient != nil {
		robotOpts = append(robotOpts, service.WithTemporalRobotClient(temporalClient))
	}
	robotSvc := service.NewRobotService(pg, redis, cmdReg, cfg.InferenceEndpoint, cfg.InferenceTimeout, robotOpts...)
	modelSvc := service.NewModelRegistryService(pg, redis)
	agentOpts := []service.AgentServiceOption{}
	if temporalClient != nil {
		agentOpts = append(agentOpts, service.WithTemporalClient(temporalClient))
	}
	agentSvc := service.NewAgentService(pg, pg, agentOpts...)

	// Training service (Kubeflow / K8s job submission)
	var jobSubmitter service.JobSubmitter
	if cfg.TrainingEnabled {
		jobSubmitter = service.NewK8sJobSubmitter(
			cfg.TrainingImage, cfg.S3Endpoint, cfg.S3Bucket,
			cfg.S3AccessKey, cfg.S3SecretKey, cfg.TrainingCallbackURL,
			cfg.TrainingNamespace,
		)
		slog.Info("training enabled", "image", cfg.TrainingImage, "namespace", cfg.TrainingNamespace)
	} else {
		jobSubmitter = &service.NoOpSubmitter{}
	}
	trainingSvc := service.NewTrainingService(pg, jobSubmitter)

	// Billing service (Temporal-backed)
	billingSvc := service.NewTemporalBillingService(pg, redis, temporalClient)

	// Additional services
	webhookSvc := service.NewWebhookService(pg)
	safetySvc := service.NewSafetyService(pg)
	skillsSvc := service.NewSkillsService(pg)
	auditLog := middleware.NewAuditLog(pg)

	// Background fleet gauge refresh — keeps fleetos_robots_total accurate in Grafana
	// without requiring a client to poll /api/v1/fleet/metrics.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := robotSvc.RefreshFleetGauges(ctx); err != nil {
					slog.Warn("failed to refresh fleet gauges", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// HTTP handlers (thin adapters)
	handler := api.NewHandler(robotSvc, redis, apiKeys)
	modelHandler := api.NewModelHandler(modelSvc)
	agentHandler := api.NewAgentHandler(agentSvc, redis)

	// Wire Kafka consumer for deployment event SSE streaming
	deployEventConsumer := ingestion.NewKafkaConsumer(cfg.KafkaBrokers, cfg.KafkaDeploymentEventTopic, "fleetos-deploy-stream")
	defer deployEventConsumer.Close()
	agentHandler.SetDeploymentEventConsumer(deployEventConsumer)

	trainingHandler := api.NewTrainingHandler(trainingSvc)
	webhookHandler := api.NewWebhookHandler(webhookSvc)
	safetyHandler := api.NewSafetyHandler(safetySvc)
	skillsHandler := api.NewSkillsHandler(skillsSvc)
	billingHandler := api.NewBillingHandler(billingSvc)
	adminSvc := service.NewAdminService(pg, pg, billingSvc)
	adminHandler := api.NewAdminHandler(adminSvc)
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
	authedMux.HandleFunc("GET /api/v1/robots/{id}/commands", handler.GetCommandHistory)
	authedMux.HandleFunc("GET /api/v1/robots/{id}/telemetry", handler.GetTelemetry)
	authedMux.HandleFunc("POST /api/v1/inference", handler.RunInference)
authedMux.HandleFunc("GET /api/v1/fleet/metrics", handler.GetFleetMetrics)
	authedMux.HandleFunc("GET /api/v1/usage", handler.GetUsage)
	// Model registry routes
	authedMux.HandleFunc("POST /api/v1/models", modelHandler.RegisterModel)
	authedMux.HandleFunc("GET /api/v1/models", modelHandler.ListModels)
	authedMux.HandleFunc("GET /api/v1/models/{id}", modelHandler.GetModel)
	authedMux.HandleFunc("POST /api/v1/models/{id}/deploy", modelHandler.DeployModel)
	authedMux.HandleFunc("POST /api/v1/models/{id}/archive", modelHandler.ArchiveModel)
	// Agent platform routes
	authedMux.HandleFunc("POST /api/v1/agents", agentHandler.RegisterAgent)
	authedMux.HandleFunc("GET /api/v1/agents", agentHandler.ListAgents)
	authedMux.HandleFunc("GET /api/v1/agents/{id}", agentHandler.GetAgent)
	authedMux.HandleFunc("POST /api/v1/agents/{id}/deploy", agentHandler.DeployAgent)
	authedMux.HandleFunc("POST /api/v1/agents/{id}/rollback", agentHandler.RollbackAgent)
	authedMux.HandleFunc("GET /api/v1/agents/{id}/deployments", agentHandler.ListDeployments)
	authedMux.HandleFunc("GET /api/v1/deployments/{id}", agentHandler.GetDeployment)
	authedMux.HandleFunc("GET /api/v1/deployments/{id}/stream", agentHandler.StreamDeployment)
	// Training API routes (Cyclotron-style)
	authedMux.HandleFunc("POST /api/v1/locomotion/jobs", trainingHandler.SubmitJob)
	authedMux.HandleFunc("GET /api/v1/locomotion/jobs", trainingHandler.ListJobs)
	authedMux.HandleFunc("GET /api/v1/locomotion/jobs/{id}", trainingHandler.GetJob)
	authedMux.HandleFunc("POST /api/v1/locomotion/evals", trainingHandler.SubmitEval)
	authedMux.HandleFunc("GET /api/v1/locomotion/evals", trainingHandler.ListEvals)
	authedMux.HandleFunc("GET /api/v1/locomotion/evals/{id}", trainingHandler.GetEval)
	// Webhook routes
	authedMux.HandleFunc("POST /api/v1/webhooks", webhookHandler.RegisterWebhook)
	authedMux.HandleFunc("GET /api/v1/webhooks", webhookHandler.ListWebhooks)
	authedMux.HandleFunc("DELETE /api/v1/webhooks/{id}", webhookHandler.DeleteWebhook)
	// Safety incident routes
	authedMux.HandleFunc("GET /api/v1/safety/incidents", safetyHandler.ListIncidents)
	authedMux.HandleFunc("POST /api/v1/safety/incidents", safetyHandler.ReportIncident)
	// Admin routes (require admin role)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("POST /api/v1/admin/tenants", adminHandler.CreateTenant)
	adminMux.HandleFunc("GET /api/v1/admin/tenants", adminHandler.ListTenants)
	adminMux.HandleFunc("GET /api/v1/admin/tenants/{id}", adminHandler.GetTenant)
	adminMux.HandleFunc("PUT /api/v1/admin/tenants/{id}", adminHandler.UpdateTenant)
	adminMux.HandleFunc("POST /api/v1/admin/tenants/{id}/keys", adminHandler.CreateAPIKey)
	adminMux.HandleFunc("GET /api/v1/admin/tenants/{id}/keys", adminHandler.ListAPIKeys)
	adminMux.HandleFunc("DELETE /api/v1/admin/keys/{hash}", adminHandler.RevokeAPIKey)
	authedMux.Handle("/api/v1/admin/", auth.RequireRole(auth.RoleAdmin)(adminMux))
	// Billing routes
	authedMux.HandleFunc("GET /api/v1/billing/subscription", billingHandler.GetSubscription)
	authedMux.HandleFunc("PUT /api/v1/billing/subscription/tier", billingHandler.ChangeTier)
	authedMux.HandleFunc("GET /api/v1/billing/invoices", billingHandler.ListInvoices)
	authedMux.HandleFunc("GET /api/v1/billing/invoices/{id}", billingHandler.GetInvoice)
	authedMux.HandleFunc("POST /api/v1/billing/invoices/{id}/retry-payment", billingHandler.RetryPayment)
	authedMux.HandleFunc("POST /api/v1/billing/cycle", billingHandler.StartBillingCycle)
	// Motor skills catalog routes
	authedMux.HandleFunc("GET /api/v1/skills", skillsHandler.ListSkills)
	authedMux.HandleFunc("GET /api/v1/skills/{id}", skillsHandler.GetSkill)
	// Analytics routes (only if ClickHouse is available)
	if analyticsHandler != nil {
		authedMux.HandleFunc("GET /api/v1/analytics/fleet", analyticsHandler.GetFleetAnalytics)
		authedMux.HandleFunc("GET /api/v1/analytics/robots/{id}", analyticsHandler.GetRobotAnalytics)
		authedMux.HandleFunc("GET /api/v1/analytics/anomalies", analyticsHandler.GetAnomalies)
	}
	// Stack middleware: CORS → Logging → Audit → Auth → RateLimit → Quota → Usage → Handler
	tierResolver := func(tenantID string) string {
		tenant, err := pg.GetTenant(context.Background(), tenantID)
		if err != nil {
			return "free" // safe default if tenant not found
		}
		return tenant.Tier
	}
	authed := auth.AuthMiddleware(tokenSvc, apiKeys)(
		auditLog.Middleware(
			middleware.RateLimiter(redis, cfg.RateLimitRPS, cfg.RateLimitBurst)(
				middleware.QuotaEnforcement(redis, tierResolver)(
					middleware.UsageMetering(redis)(authedMux),
				),
			),
		),
	)

	mux.Handle("/api/", authed)

	// Internal callback endpoints (no auth — called by training pods inside the cluster)
	mux.HandleFunc("POST /api/v1/internal/training/callback", trainingHandler.TrainingCallback)
	mux.HandleFunc("POST /api/v1/internal/eval/callback", trainingHandler.EvalCallback)

	// WebSocket endpoint (no auth — browser WebSocket can't set headers)
	mux.HandleFunc("GET /api/v1/ws/telemetry", handler.WebSocketTelemetry)

	// Admin web console (SPA served from filesystem)
	adminDistPath := "admin-web/dist"
	if _, err := os.Stat(adminDistPath); err == nil {
		slog.Info("serving admin console", "path", adminDistPath)
		mux.Handle("/admin/", http.StripPrefix("/admin/", spaHandler(http.Dir(adminDistPath))))
	} else {
		slog.Info("admin console not built, skipping /admin/ routes (run: cd admin-web && npm run build)")
	}

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
		auditLog.Shutdown(shutdownCtx)
	}()

	slog.Info("api server starting", "port", cfg.HTTPPort)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
