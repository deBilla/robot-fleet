package simulator

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Fleet manages a collection of simulated robots and streams their telemetry.
type Fleet struct {
	robots       []*Robot
	robotMap     map[string]*Robot
	tickInterval time.Duration
	grpcTarget   string
	redisAddr    string
}

func NewFleet(count int, tickInterval time.Duration, grpcTarget, redisAddr string) *Fleet {
	robots := make([]*Robot, count)
	robotMap := make(map[string]*Robot, count)
	for i := range count {
		robots[i] = NewRobot(i)
		robotMap[robots[i].ID] = robots[i]
	}
	return &Fleet{
		robots:       robots,
		robotMap:     robotMap,
		tickInterval: tickInterval,
		grpcTarget:   grpcTarget,
		redisAddr:    redisAddr,
	}
}

// Run starts the simulation loop and streams telemetry to the ingestion service.
func (f *Fleet) Run(ctx context.Context) {
	conn, err := grpc.NewClient(f.grpcTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		slog.Error("failed to connect to ingestion service", "error", err, "target", f.grpcTarget)
		return
	}
	defer conn.Close()

	client := pb.NewTelemetryServiceClient(conn)

	stream, err := client.StreamTelemetry(ctx)
	if err != nil {
		slog.Error("failed to open telemetry stream", "error", err)
		return
	}

	// Start ack receiver — exits when stream closes or context cancels
	go func() {
		for {
			ack, err := stream.Recv()
			if err != nil {
				return // Stream closed or error — goroutine exits
			}
			slog.Debug("received ack", "message_id", ack.MessageId, "success", ack.Success)
		}
	}()

	// Subscribe to Redis for commands
	if f.redisAddr != "" {
		go f.subscribeCommands(ctx)
	}

	ticker := time.NewTicker(f.tickInterval)
	defer ticker.Stop()

	lidarTicker := time.NewTicker(f.tickInterval * 10) // LiDAR at 1/10th rate
	defer lidarTicker.Stop()

	videoTicker := time.NewTicker(f.tickInterval * 3) // Video at 1/3rd rate
	defer videoTicker.Stop()

	var mu sync.Mutex
	sendPacket := func(pkt *pb.TelemetryPacket) {
		mu.Lock()
		defer mu.Unlock()
		if err := stream.Send(pkt); err != nil {
			slog.Warn("failed to send telemetry", "robot", pkt.RobotId, "error", err)
		}
	}

	slog.Info("fleet simulation started", "robots", len(f.robots))

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			stream.CloseSend()
			mu.Unlock()
			return

		case <-ticker.C:
			for _, r := range f.robots {
				r.Step()
				sendPacket(r.ToTelemetryPacket())
			}

		case <-lidarTicker.C:
			for _, r := range f.robots {
				sendPacket(r.GenerateLidarScan(360))
			}

		case <-videoTicker.C:
			for _, r := range f.robots {
				sendPacket(r.GenerateVideoFrame())
			}
		}
	}
}

// subscribeCommands listens for commands on Redis pub/sub and dispatches to robots.
func (f *Fleet) subscribeCommands(ctx context.Context) {
	client := redis.NewClient(&redis.Options{Addr: f.redisAddr})
	defer client.Close()

	// Subscribe to command channels for all robots
	channels := make([]string, 0, len(f.robots))
	for _, r := range f.robots {
		channels = append(channels, "commands:"+r.ID)
	}

	sub := client.Subscribe(ctx, channels...)
	defer sub.Close()

	slog.Info("subscribed to command channels", "count", len(channels))

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var cmd struct {
				RobotID string         `json:"robot_id"`
				Command struct {
					Type   string         `json:"type"`
					Params map[string]any `json:"params"`
				} `json:"command"`
			}
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				slog.Error("failed to parse command", "error", err)
				continue
			}

			robot, ok := f.robotMap[cmd.RobotID]
			if !ok {
				slog.Warn("command for unknown robot", "robot", cmd.RobotID)
				continue
			}

			robot.ApplyCommand(cmd.Command.Type, cmd.Command.Params)
			slog.Info("command applied", "robot", cmd.RobotID, "type", cmd.Command.Type, "params", cmd.Command.Params)
		}
	}
}
