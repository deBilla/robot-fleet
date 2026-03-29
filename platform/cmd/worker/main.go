package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.temporal.io/sdk/worker"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/store"
	temporalpkg "github.com/dimuthu/robot-fleet/internal/temporal"
	"github.com/dimuthu/robot-fleet/internal/temporal/activities"
	"github.com/dimuthu/robot-fleet/internal/temporal/workflows"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down worker")
		cancel()
	}()

	// Connect stores
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

	// Temporal client
	tc, err := temporalpkg.NewClient(cfg.TemporalHostPort, cfg.TemporalNamespace)
	if err != nil {
		slog.Error("failed to connect to temporal", "error", err)
		os.Exit(1)
	}
	defer tc.Close()

	// Activity instances with injected dependencies
	cmdActs := &activities.CommandActivities{Repo: pg, Cache: redis}
	deployActs := &activities.DeploymentActivities{Models: pg}
	agentDeployActs := &activities.AgentDeploymentActivities{Agents: pg, Deployments: pg, Cache: redis}
	webhookActs := activities.NewWebhookActivities()

	// Command worker
	cmdWorker := worker.New(tc, temporalpkg.TaskQueueCommand, worker.Options{})
	cmdWorker.RegisterWorkflow(workflows.CommandDispatchWorkflow)
	cmdWorker.RegisterActivity(cmdActs)

	// Deployment worker (model + agent deployment workflows share the same queue)
	deployWorker := worker.New(tc, temporalpkg.TaskQueueDeployment, worker.Options{})
	deployWorker.RegisterWorkflow(workflows.ModelDeploymentWorkflow)
	deployWorker.RegisterWorkflow(workflows.AgentDeploymentWorkflow)
	deployWorker.RegisterActivity(deployActs)
	deployWorker.RegisterActivity(agentDeployActs)

	// Webhook worker
	whWorker := worker.New(tc, temporalpkg.TaskQueueWebhook, worker.Options{})
	whWorker.RegisterWorkflow(workflows.WebhookFanoutWorkflow)
	whWorker.RegisterWorkflow(workflows.WebhookDeliverWorkflow)
	whWorker.RegisterActivity(webhookActs)

	// Start Kafka-Temporal ack bridge (Redis subscriber → Temporal signal)
	go temporalpkg.AckBridge(ctx, redis.RedisClient(), tc)

	// Start all workers
	slog.Info("starting temporal workers",
		"command_queue", temporalpkg.TaskQueueCommand,
		"deployment_queue", temporalpkg.TaskQueueDeployment,
		"webhook_queue", temporalpkg.TaskQueueWebhook,
	)

	errCh := make(chan error, 3)
	go func() { errCh <- cmdWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- deployWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- whWorker.Run(worker.InterruptCh()) }()

	// Wait for first error or shutdown
	select {
	case err := <-errCh:
		if err != nil {
			slog.Error("worker error", "error", err)
		}
	case <-ctx.Done():
	}

	slog.Info("worker stopped")
}
