// simulate_telemetry is a lightweight telemetry generator for testing the
// platform without the full playground simulator. It streams synthetic
// robot state packets via gRPC to the ingestion service.
//
// Usage:
//
//	go run scripts/simulate_telemetry.go -target localhost:50051 -robots 5 -rate 10
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var jointNames = []string{
	"head_yaw", "head_pitch",
	"l_shoulder_pitch", "l_shoulder_roll", "l_elbow", "l_wrist",
	"r_shoulder_pitch", "r_shoulder_roll", "r_elbow", "r_wrist",
	"torso_yaw",
	"l_hip_yaw", "l_hip_roll", "l_hip_pitch", "l_knee", "l_ankle_pitch", "l_ankle_roll",
	"r_hip_yaw", "r_hip_roll", "r_hip_pitch", "r_knee", "r_ankle_pitch", "r_ankle_roll",
}

func main() {
	target := flag.String("target", "localhost:50051", "gRPC ingestion address")
	robots := flag.Int("robots", 5, "number of simulated robots")
	rateHz := flag.Int("rate", 10, "telemetry packets per second per robot")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	conn, err := grpc.NewClient(*target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		slog.Error("failed to connect", "target", *target, "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewTelemetryServiceClient(conn)
	stream, err := client.StreamTelemetry(ctx)
	if err != nil {
		slog.Error("failed to open stream", "error", err)
		os.Exit(1)
	}

	// Drain acks in background
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()

	slog.Info("streaming synthetic telemetry", "target", *target, "robots", *robots, "rate_hz", *rateHz)

	ticker := time.NewTicker(time.Second / time.Duration(*rateHz))
	defer ticker.Stop()

	step := 0
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			slog.Info("stopped", "total_steps", step)
			return
		case <-ticker.C:
			for i := range *robots {
				pkt := generatePacket(i, step)
				if err := stream.Send(pkt); err != nil {
					slog.Warn("send failed", "error", err)
					return
				}
			}
			step++
			if step%(*rateHz*10) == 0 {
				slog.Info("heartbeat", "step", step, "packets_sent", step*(*robots))
			}
		}
	}
}

func generatePacket(robotIdx, step int) *pb.TelemetryPacket {
	t := float64(step) * 0.1
	id := fmt.Sprintf("robot-%04d", robotIdx)

	// Sinusoidal joint positions — simple but realistic-looking
	joints := make([]*pb.JointState, len(jointNames))
	for j, name := range jointNames {
		phase := float64(j) * 0.5
		joints[j] = &pb.JointState{
			Name:     name,
			Position: 0.3 * math.Sin(t*1.5+phase),
			Velocity: 0.45 * math.Cos(t*1.5+phase),
			Torque:   2.0 + 1.0*math.Sin(t+phase),
		}
	}

	// Battery: slow drain with periodic recharge cycles
	batteryPhase := math.Mod(t/300.0+float64(robotIdx)*0.1, 1.0) // 5-min cycle
	battery := 0.3 + 0.7*math.Abs(math.Sin(batteryPhase*math.Pi))

	// Position: gentle wander
	px := 5.0*math.Sin(t*0.02+float64(robotIdx)) + rand.Float64()*0.01
	py := 5.0*math.Cos(t*0.02+float64(robotIdx)*0.7) + rand.Float64()*0.01

	// Status rotation
	statuses := []string{"active", "active", "active", "idle", "charging"}
	status := statuses[(step/100+robotIdx)%len(statuses)]

	return &pb.TelemetryPacket{
		RobotId:   id,
		Timestamp: timestamppb.Now(),
		Payload: &pb.TelemetryPacket_State{
			State: &pb.RobotState{
				RobotId:      id,
				BatteryLevel: battery,
				Status:       status,
				Timestamp:    timestamppb.Now(),
				Pose: &pb.Pose{
					Position:    &pb.Vector3{X: px, Y: py, Z: 0.95},
					Orientation: &pb.Quaternion{W: 1, X: 0, Y: 0, Z: 0},
				},
				Joints: joints,
			},
		},
	}
}
