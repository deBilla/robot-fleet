// Package integration contains integration tests that require external services.
// Run with: go test -tags=integration ./test/integration/
package integration

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/simulator"
	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// mockTelemetryServer collects packets for testing.
type mockTelemetryServer struct {
	pb.UnimplementedTelemetryServiceServer
	packets []*pb.TelemetryPacket
}

func (s *mockTelemetryServer) StreamTelemetry(stream pb.TelemetryService_StreamTelemetryServer) error {
	for {
		pkt, err := stream.Recv()
		if err != nil {
			return nil
		}
		s.packets = append(s.packets, pkt)
		stream.Send(&pb.StreamAck{MessageId: pkt.RobotId, Success: true})
	}
}

func TestSimulatorToGRPC_Integration(t *testing.T) {
	// Start mock gRPC server
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	mock := &mockTelemetryServer{}
	server := grpc.NewServer()
	pb.RegisterTelemetryServiceServer(server, mock)

	go server.Serve(lis)
	defer server.GracefulStop()

	target := lis.Addr().String()

	// Create and run fleet
	fleet := simulator.NewFleet(3, 50*time.Millisecond, target, "")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	fleet.Run(ctx)

	if len(mock.packets) == 0 {
		t.Fatal("expected to receive telemetry packets")
	}

	// Verify packets have valid structure
	for _, pkt := range mock.packets[:min(5, len(mock.packets))] {
		if pkt.RobotId == "" {
			t.Error("packet should have a robot_id")
		}
		if pkt.Timestamp == nil {
			t.Error("packet should have a timestamp")
		}
	}

	t.Logf("received %d telemetry packets from 3 robots", len(mock.packets))
}

func TestProtobufSerialization(t *testing.T) {
	r := simulator.NewRobot(1)
	r.Step()

	pkt := r.ToTelemetryPacket()

	// Serialize
	data, err := proto.Marshal(pkt)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Deserialize
	var decoded pb.TelemetryPacket
	if err := proto.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.RobotId != pkt.RobotId {
		t.Errorf("robot_id mismatch: %s vs %s", decoded.RobotId, pkt.RobotId)
	}

	state := decoded.GetState()
	if state == nil {
		t.Fatal("decoded packet should have state payload")
	}
	if len(state.Joints) != len(simulator.JointNames) {
		t.Errorf("expected %d joints, got %d", len(simulator.JointNames), len(state.Joints))
	}
}

func TestGRPCClientConnection(t *testing.T) {
	// Start a minimal gRPC server
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	pb.RegisterTelemetryServiceServer(server, &pb.UnimplementedTelemetryServiceServer{})
	go server.Serve(lis)
	defer server.GracefulStop()

	// Test client connection
	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := pb.NewTelemetryServiceClient(conn)
	if client == nil {
		t.Fatal("client should not be nil")
	}
}

func TestConfig_LoadsCorrectly(t *testing.T) {
	t.Setenv("ENVIRONMENT", "test")
	t.Setenv("SIM_ROBOT_COUNT", "25")
	t.Setenv("GRPC_PORT", "50052")

	cfg := config.Load()

	if cfg.Environment != "test" {
		t.Errorf("expected test, got %s", cfg.Environment)
	}
	if cfg.SimRobotCount != 25 {
		t.Errorf("expected 25, got %d", cfg.SimRobotCount)
	}
	if cfg.GRPCPort != 50052 {
		t.Errorf("expected 50052, got %d", cfg.GRPCPort)
	}
}

func TestSimulatorRobots_ProduceValidTelemetry(t *testing.T) {
	robots := make([]*simulator.Robot, 10)
	for i := range robots {
		robots[i] = simulator.NewRobot(i)
	}

	// Run 100 steps and verify all packets are valid
	for step := range 100 {
		for _, r := range robots {
			r.Step()
			pkt := r.ToTelemetryPacket()

			// Verify serialization roundtrip
			data, err := proto.Marshal(pkt)
			if err != nil {
				t.Fatalf("step %d: marshal failed for %s: %v", step, r.ID, err)
			}
			if len(data) == 0 {
				t.Errorf("step %d: empty serialized data for %s", step, r.ID)
			}

			var decoded pb.TelemetryPacket
			if err := proto.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("step %d: unmarshal failed for %s: %v", step, r.ID, err)
			}

			state := decoded.GetState()
			if state == nil {
				t.Fatalf("step %d: missing state for %s", step, r.ID)
			}

			if state.BatteryLevel < 0 || state.BatteryLevel > 1 {
				t.Errorf("step %d: battery out of range for %s: %f", step, r.ID, state.BatteryLevel)
			}
		}
	}
}

func TestLidarScan_VariousPointCounts(t *testing.T) {
	r := simulator.NewRobot(0)

	counts := []int{0, 1, 10, 360, 1000}
	for _, count := range counts {
		t.Run(fmt.Sprintf("points_%d", count), func(t *testing.T) {
			pkt := r.GenerateLidarScan(count)
			scan := pkt.GetLidar()
			if scan == nil {
				t.Fatal("expected lidar payload")
			}
			if len(scan.Points) != count {
				t.Errorf("expected %d points, got %d", count, len(scan.Points))
			}
		})
	}
}
