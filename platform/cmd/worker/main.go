package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.temporal.io/sdk/worker"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/ingestion"
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

	// Kafka producers for command dispatch and deployment events
	cmdDispatchProducer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaCommandDispatchTopic)
	if err != nil {
		slog.Warn("kafka command dispatch producer not available, falling back to Redis", "error", err)
	} else {
		defer cmdDispatchProducer.Close()
	}

	deployEventProducer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaDeploymentEventTopic)
	if err != nil {
		slog.Warn("kafka deployment event producer not available, falling back to Redis", "error", err)
	} else {
		defer deployEventProducer.Close()
	}

	// Kafka consumer for command acks (replaces Redis ack bridge)
	ackConsumer := ingestion.NewKafkaConsumer(cfg.KafkaBrokers, cfg.KafkaCommandAcksTopic, "fleetos-ack-bridge")
	defer ackConsumer.Close()

	// Activity instances with injected dependencies
	cmdActs := &activities.CommandActivities{Repo: pg, Cache: redis, Publisher: cmdDispatchProducer}
	deployActs := &activities.DeploymentActivities{Models: pg, Robots: pg}
	agentDeployActs := &activities.AgentDeploymentActivities{Agents: pg, Deployments: pg, Cache: redis, EventProducer: deployEventProducer}
	webhookActs := activities.NewWebhookActivities()
	billingActs := &activities.BillingActivities{Billing: pg, Cache: redis}

	// Inference activity
	inferenceActs := &activities.InferenceActivities{
		Endpoint: cfg.InferenceEndpoint,
		Timeout:  cfg.InferenceTimeout,
	}

	// Command worker
	cmdWorker := worker.New(tc, temporalpkg.TaskQueueCommand, worker.Options{})
	cmdWorker.RegisterWorkflow(workflows.CommandDispatchWorkflow)
	cmdWorker.RegisterActivity(cmdActs)
	cmdWorker.RegisterActivity(inferenceActs)

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

	// Billing worker
	billingWorker := worker.New(tc, temporalpkg.TaskQueueBilling, worker.Options{})
	billingWorker.RegisterWorkflow(workflows.BillingCycleWorkflow)
	billingWorker.RegisterActivity(billingActs)

	// Training pipeline worker
	trainingActs := &activities.TrainingActivities{Models: pg, Robots: pg}
	trainingWorker := worker.New(tc, temporalpkg.TaskQueueTraining, worker.Options{})
	trainingWorker.RegisterWorkflow(workflows.TrainingPipelineWorkflow)
	trainingWorker.RegisterWorkflow(workflows.ModelDeploymentWorkflow) // child workflow for auto-deploy
	trainingWorker.RegisterActivity(trainingActs)
	trainingWorker.RegisterActivity(deployActs) // needed for canary child workflow

	// Start Kafka-Temporal ack bridge (Kafka consumer → Temporal signal)
	go temporalpkg.AckBridgeKafka(ctx, ackConsumer, tc)

	// Start all workers
	slog.Info("starting temporal workers",
		"command_queue", temporalpkg.TaskQueueCommand,
		"deployment_queue", temporalpkg.TaskQueueDeployment,
		"webhook_queue", temporalpkg.TaskQueueWebhook,
		"billing_queue", temporalpkg.TaskQueueBilling,
		"training_queue", temporalpkg.TaskQueueTraining,
	)

	errCh := make(chan error, 5)
	go func() { errCh <- cmdWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- deployWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- whWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- billingWorker.Run(worker.InterruptCh()) }()
	go func() { errCh <- trainingWorker.Run(worker.InterruptCh()) }()

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
