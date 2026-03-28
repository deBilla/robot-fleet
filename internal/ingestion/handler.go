package ingestion

import (
	"io"
	"log/slog"
	"sync/atomic"

	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"google.golang.org/protobuf/proto"
)

// TelemetryHandler implements the gRPC TelemetryService.
type TelemetryHandler struct {
	pb.UnimplementedTelemetryServiceServer
	producer     *KafkaProducer
	packetCount  atomic.Int64
}

func NewTelemetryHandler(producer *KafkaProducer) *TelemetryHandler {
	return &TelemetryHandler{producer: producer}
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

// StreamCommands sends commands to a specific robot (stub for now).
func (h *TelemetryHandler) StreamCommands(req *pb.CommandRequest, stream pb.TelemetryService_StreamCommandsServer) error {
	slog.Info("command stream opened", "robot", req.RobotId)
	// Block until context is cancelled — commands will be pushed from the API layer
	<-stream.Context().Done()
	return nil
}
