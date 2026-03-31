package ingestion

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
)

// CommandDispatcher consumes commands from Kafka and routes them to per-robot channels.
// One consumer, many robot streams.
type CommandDispatcher struct {
	consumer *KafkaConsumer
	mu       sync.RWMutex
	robots   map[string]chan []byte // robot_id → command channel
}

// NewCommandDispatcher creates a dispatcher backed by a Kafka consumer.
func NewCommandDispatcher(consumer *KafkaConsumer) *CommandDispatcher {
	return &CommandDispatcher{
		consumer: consumer,
		robots:   make(map[string]chan []byte),
	}
}

// Start begins consuming from Kafka and routing to registered robots.
// Blocks until ctx is cancelled.
func (d *CommandDispatcher) Start(ctx context.Context) {
	slog.Info("command dispatcher started")
	err := d.consumer.Consume(ctx, func(key, value []byte) error {
		robotID := string(key)

		d.mu.RLock()
		ch, ok := d.robots[robotID]
		d.mu.RUnlock()

		if !ok {
			// Robot not connected — skip (command will be retried by Temporal if no ack)
			slog.Debug("no stream for robot, skipping command", "robot", robotID)
			return nil
		}

		select {
		case ch <- value:
		default:
			slog.Warn("command channel full, dropping", "robot", robotID)
		}
		return nil
	})
	if err != nil {
		slog.Error("command dispatcher error", "error", err)
	}
}

// Subscribe registers a robot and returns a channel that receives commands.
// Caller must call Unsubscribe when the robot disconnects.
func (d *CommandDispatcher) Subscribe(robotID string) <-chan []byte {
	ch := make(chan []byte, 16)
	d.mu.Lock()
	d.robots[robotID] = ch
	d.mu.Unlock()
	slog.Info("robot subscribed to command dispatch", "robot", robotID)
	return ch
}

// Unsubscribe removes a robot's command channel.
func (d *CommandDispatcher) Unsubscribe(robotID string) {
	d.mu.Lock()
	if ch, ok := d.robots[robotID]; ok {
		close(ch)
		delete(d.robots, robotID)
	}
	d.mu.Unlock()
	slog.Info("robot unsubscribed from command dispatch", "robot", robotID)
}

// commandPayload is used for parsing the robot_id from command JSON.
type commandPayload struct {
	RobotID string `json:"robot_id"`
}

func parseRobotID(data []byte) string {
	var p commandPayload
	_ = json.Unmarshal(data, &p)
	return p.RobotID
}
