package ingestion

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TelemetryHandler implements the gRPC TelemetryService.
type TelemetryHandler struct {
	pb.UnimplementedTelemetryServiceServer
	producer     *KafkaProducer
	redisClient  *redis.Client
	packetCount  atomic.Int64
}

func NewTelemetryHandler(producer *KafkaProducer, redisClient *redis.Client) *TelemetryHandler {
	return &TelemetryHandler{producer: producer, redisClient: redisClient}
}

// StreamTelemetry receives a bidirectional stream of telemetry from robots.
func (h *TelemetryHandler) StreamTelemetry(stream pb.TelemetryService_StreamTelemetryServer) error {
	slog.Info("new telemetry stream connected")

	for {
		packet, err := stream.Recv()
		if err == io.EOF {
			slog.Info("telemetry stream closed by client")
			return nil
		}
		if err != nil {
			slog.Error("telemetry stream error", "error", err)
			return err
		}

		count := h.packetCount.Add(1)
		if count%1000 == 0 {
			slog.Info("telemetry packets received", "total", count, "robot", packet.RobotId)
		}

		// Serialize and publish to Kafka
		data, err := proto.Marshal(packet)
		if err != nil {
			slog.Error("failed to marshal telemetry", "error", err)
			continue
		}

		if err := h.producer.Publish(packet.RobotId, data); err != nil {
			slog.Error("failed to publish to kafka",
				"robot", packet.RobotId,
				"error", err,
			)
			if sendErr := stream.Send(&pb.StreamAck{
				MessageId: packet.RobotId,
				Success:   false,
			}); sendErr != nil {
				return sendErr
			}
			continue
		}

		if err := stream.Send(&pb.StreamAck{
			MessageId: packet.RobotId,
			Success:   true,
		}); err != nil {
			return err
		}
	}
}

// StreamCommands subscribes to the Redis command channel for a robot and forwards
// commands over the gRPC server stream. This bridges the REST API command dispatch
// (which publishes to Redis pub/sub) to the robot's persistent gRPC connection.
func (h *TelemetryHandler) StreamCommands(req *pb.CommandRequest, stream pb.TelemetryService_StreamCommandsServer) error {
	robotID := req.RobotId
	channel := "commands:" + robotID
	slog.Info("command stream opened", "robot", robotID, "channel", channel)

	ctx := stream.Context()
	sub := h.redisClient.Subscribe(ctx, channel)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			slog.Info("command stream closed", "robot", robotID)
			return nil
		case msg, ok := <-ch:
			if !ok {
				slog.Info("command subscription closed", "robot", robotID)
				return nil
			}

			var cmdPayload struct {
				CommandID  int64  `json:"command_id"`
				WorkflowID string `json:"workflow_id"` // Temporal workflow ID for ack bridge
				Command    struct {
					Type   string         `json:"type"`
					Params map[string]any `json:"params"`
				} `json:"command"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmdPayload); err != nil {
				slog.Error("failed to unmarshal command", "robot", robotID, "error", err)
				continue
			}

			robotCmd := commandToProto(robotID, cmdPayload.CommandID, cmdPayload.Command.Type, cmdPayload.Command.Params)
			if err := stream.Send(robotCmd); err != nil {
				slog.Error("failed to send command to robot", "robot", robotID, "error", err)
				return err
			}

			slog.Info("command dispatched to robot", "robot", robotID, "command_id", cmdPayload.CommandID, "type", cmdPayload.Command.Type)

			// Publish command ack event (Temporal bridge picks up workflow_id to signal workflow)
			ackEvent, _ := json.Marshal(map[string]any{
				"command_id":    cmdPayload.CommandID,
				"robot_id":      robotID,
				"status":        "dispatched",
				"workflow_id":   cmdPayload.WorkflowID,
				"dispatched_at": fmt.Sprintf("%d", time.Now().UnixNano()),
			})
			_ = h.redisClient.Publish(ctx, "command_acks", ackEvent).Err()
		}
	}
}

// commandToProto converts a JSON command into a protobuf RobotCommand.
func commandToProto(robotID string, commandID int64, cmdType string, params map[string]any) *pb.RobotCommand {
	cmd := &pb.RobotCommand{
		RobotId:   robotID,
		CommandId: fmt.Sprintf("%d", commandID),
		Timestamp: timestamppb.Now(),
	}

	switch cmdType {
	case "move":
		move := &pb.MoveCommand{MaxVelocity: 1.0}
		if tx, ok := params["x"].(float64); ok {
			move.TargetPosition = &pb.Vector3{X: tx}
		}
		if tp, ok := params["target_position"].(map[string]any); ok {
			move.TargetPosition = &pb.Vector3{}
			if x, ok := tp["x"].(float64); ok {
				move.TargetPosition.X = x
			}
			if y, ok := tp["y"].(float64); ok {
				move.TargetPosition.Y = y
			}
			if z, ok := tp["z"].(float64); ok {
				move.TargetPosition.Z = z
			}
		}
		if v, ok := params["max_velocity"].(float64); ok {
			move.MaxVelocity = v
		}
		cmd.Action = &pb.RobotCommand_Move{Move: move}
	case "stop":
		emergency := false
		if e, ok := params["emergency"].(bool); ok {
			emergency = e
		}
		cmd.Action = &pb.RobotCommand_Stop{Stop: &pb.StopCommand{Emergency: emergency}}
	default:
		// For all other command types (wave, dance, etc.), send as a joint command
		// with empty targets — the robot firmware handles named command types.
		cmd.Action = &pb.RobotCommand_Joint{Joint: &pb.JointCommand{DurationSeconds: 2.0}}
	}

	return cmd
}
