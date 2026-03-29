package temporal

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/client"
)

// AckBridge subscribes to Redis command_acks channel and signals the corresponding
// Temporal command workflow. This bridges the robot ack (via gRPC → ingestion → Redis)
// back to the durable workflow tracking the command lifecycle.
func AckBridge(ctx context.Context, redisClient *redis.Client, temporalClient client.Client) {
	sub := redisClient.Subscribe(ctx, "command_acks")
	defer sub.Close()

	slog.Info("temporal ack bridge started", "channel", "command_acks")

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
				continue // non-Temporal command, skip
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
