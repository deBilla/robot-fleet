package temporal

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/dimuthu/robot-fleet/internal/ingestion"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/client"
)

// AckBridge subscribes to command acks and signals the corresponding Temporal
// command workflow. Prefers Kafka if an ack consumer is provided, otherwise
// falls back to Redis pub/sub.
func AckBridge(ctx context.Context, redisClient *redis.Client, temporalClient client.Client) {
	AckBridgeRedis(ctx, redisClient, temporalClient)
}

// AckBridgeKafka consumes command acks from Kafka and signals Temporal workflows.
func AckBridgeKafka(ctx context.Context, consumer *ingestion.KafkaConsumer, temporalClient client.Client) {
	slog.Info("temporal ack bridge started (kafka)", "topic", "robot.command-acks")

	err := consumer.Consume(ctx, func(key, value []byte) error {
		var ack struct {
			CommandID  int64  `json:"command_id"`
			RobotID    string `json:"robot_id"`
			Status     string `json:"status"`
			WorkflowID string `json:"workflow_id"`
		}
		if err := json.Unmarshal(value, &ack); err != nil {
			slog.Error("failed to unmarshal command ack", "error", err)
			return nil // skip malformed
		}

		if ack.WorkflowID == "" {
			return nil
		}

		err := temporalClient.SignalWorkflow(ctx, ack.WorkflowID, "", "command-ack", ack.Status)
		if err != nil {
			slog.Warn("failed to signal command workflow",
				"workflow", ack.WorkflowID,
				"command_id", ack.CommandID,
				"error", err,
			)
		} else {
			slog.Debug("signaled command workflow",
				"workflow", ack.WorkflowID,
				"status", ack.Status,
			)
		}
		return nil
	})
	if err != nil {
		slog.Error("ack bridge kafka consumer error", "error", err)
	}
}

// AckBridgeRedis subscribes to Redis command_acks channel and signals Temporal workflows.
// This is the legacy fallback when Kafka ack topic is not configured.
func AckBridgeRedis(ctx context.Context, redisClient *redis.Client, temporalClient client.Client) {
	sub := redisClient.Subscribe(ctx, "command_acks")
	defer sub.Close()

	slog.Info("temporal ack bridge started (redis fallback)", "channel", "command_acks")

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			slog.Info("temporal ack bridge stopped")
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}

			var ack struct {
				CommandID  int64  `json:"command_id"`
				RobotID    string `json:"robot_id"`
				Status     string `json:"status"`
				WorkflowID string `json:"workflow_id"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &ack); err != nil {
				slog.Error("failed to unmarshal command ack", "error", err)
				continue
			}

			if ack.WorkflowID == "" {
				continue
			}

			err := temporalClient.SignalWorkflow(ctx, ack.WorkflowID, "", "command-ack", ack.Status)
			if err != nil {
				slog.Warn("failed to signal command workflow",
					"workflow", ack.WorkflowID,
					"command_id", ack.CommandID,
					"error", err,
				)
			} else {
				slog.Debug("signaled command workflow",
					"workflow", ack.WorkflowID,
					"status", ack.Status,
				)
			}
		}
	}
}
