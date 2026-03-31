package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dimuthu/robot-fleet/internal/config"
	"github.com/dimuthu/robot-fleet/internal/ingestion"
	"github.com/dimuthu/robot-fleet/internal/middleware"
	"github.com/dimuthu/robot-fleet/internal/service"
	"github.com/dimuthu/robot-fleet/internal/store"
	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	temporalpkg "github.com/dimuthu/robot-fleet/internal/temporal"
	"github.com/dimuthu/robot-fleet/internal/temporal/workflows"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/proto"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := config.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down processor")
		cancel()
	}()

	// Connect to PostgreSQL (robot metadata only — telemetry goes to S3)
	pg, err := store.NewPostgresStore(ctx, cfg.PostgresDSN)
	if err != nil {
		slog.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer pg.Close()

	// Connect to Redis
	redis, err := store.NewRedisStore(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		slog.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// Connect to S3/MinIO for raw telemetry storage
	s3, err := store.NewS3Store(ctx, store.S3Config{
		Endpoint:  cfg.S3Endpoint,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		UseSSL:    cfg.S3UseSSL,
	})
	if err != nil {
		slog.Error("failed to connect to s3", "error", err)
		os.Exit(1)
	}
	_ = s3 // used below in telemetry handler

	// Create Kafka consumer
	consumer := ingestion.NewKafkaConsumer(cfg.KafkaBrokers, cfg.KafkaTelemetryTopic, cfg.KafkaGroupID)
	defer consumer.Close()

	// Expose Prometheus metrics on HTTP :9090
	procMetricsMux := http.NewServeMux()
	procMetricsMux.Handle("/metrics", middleware.MetricsHandler())
	procMetricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	go func() {
		slog.Info("processor metrics server starting", "addr", ":9090")
		if err := http.ListenAndServe(":9090", procMetricsMux); err != nil {
			slog.Error("processor metrics server error", "error", err)
		}
	}()

	slog.Info("stream processor starting",
		"brokers", cfg.KafkaBrokers,
		"topic", cfg.KafkaTelemetryTopic,
		"group", cfg.KafkaGroupID,
	)

	// DLQ producer for failed messages
	dlqProducer, err := ingestion.NewKafkaProducer(cfg.KafkaBrokers, cfg.KafkaTelemetryTopic+".dlq")
	if err != nil {
		slog.Warn("failed to create DLQ producer, failed messages will be dropped", "error", err)
	} else {
		defer dlqProducer.Close()
	}

	// Command consumer: reads from robot.commands Kafka topic, starts Temporal workflows
	if cfg.TemporalEnabled {
		tc, err := temporalpkg.NewClient(cfg.TemporalHostPort, cfg.TemporalNamespace)
		if err != nil {
			slog.Error("failed to connect to temporal", "error", err)
			os.Exit(1)
		}
		defer tc.Close()

		cmdConsumer := ingestion.NewKafkaConsumer(cfg.KafkaBrokers, cfg.KafkaCommandTopic, "fleetos-command-processor")
		defer cmdConsumer.Close()

		slog.Info("command consumer starting",
			"topic", cfg.KafkaCommandTopic,
			"group", "fleetos-command-processor",
		)

		go func() {
			err := cmdConsumer.Consume(ctx, func(key, value []byte) error {
				var msg service.CommandMessage
				if err := json.Unmarshal(value, &msg); err != nil {
					slog.Error("failed to unmarshal command message", "error", err)
					return nil // skip malformed messages
				}

				workflowID := fmt.Sprintf("cmd-%s-%s", msg.RobotID, msg.DedupKey)
				_, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
					ID:        workflowID,
					TaskQueue: temporalpkg.TaskQueueCommand,
				}, workflows.CommandDispatchWorkflow, workflows.CommandWorkflowInput{
					RobotID:              msg.RobotID,
					CommandID:            msg.CommandID,
					CmdType:              msg.CmdType,
					Params:               msg.Params,
					TenantID:             msg.TenantID,
					DedupKey:             msg.DedupKey,
					WithInference:        msg.WithInference,
					InferenceInstruction: cmdInferenceInstruction(msg),
					InferenceModelID:     cmdInferenceModelID(msg),
				})
				if err != nil {
					if strings.Contains(err.Error(), "already started") {
						slog.Debug("duplicate command workflow skipped", "workflow", workflowID)
						return nil
					}
					slog.Error("failed to start command workflow", "workflow", workflowID, "error", err)
					return nil // don't block consumer on workflow errors
				}
				slog.Info("command workflow started", "workflow", workflowID, "robot", msg.RobotID, "type", msg.CmdType)
				return nil
			})
			if err != nil {
				slog.Error("command consumer error", "error", err)
			}
		}()
	}

	// Background: aggregate robot performance metrics → model registry success_rate
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				since := time.Now().Add(-10 * time.Minute)
				robots, err := pg.ListAllActiveRobots(ctx, since, 10000)
				if err != nil || len(robots) == 0 {
					continue
				}
				// Aggregate uptime_pct from hot state for each robot with a model
				modelUptimes := make(map[string][]float64)
				for _, r := range robots {
					if r.InferenceModelID == "" {
						continue
					}
					hs, err := redis.GetRobotState(ctx, r.ID)
					if err != nil || hs == nil {
						continue
					}
					modelUptimes[r.InferenceModelID] = append(modelUptimes[r.InferenceModelID], hs.UptimePct)
				}
				// Update model registry success_rate
				for modelID, uptimes := range modelUptimes {
					if len(uptimes) == 0 {
						continue
					}
					var sum float64
					for _, u := range uptimes {
						sum += u
					}
					avgUptime := sum / float64(len(uptimes))
					model, err := pg.GetModel(ctx, modelID)
					if err != nil {
						continue
					}
					if model.Metrics == nil {
						model.Metrics = make(map[string]any)
					}
					model.Metrics["success_rate"] = avgUptime
					model.Metrics["robot_count"] = len(uptimes)
					model.Metrics["updated_at"] = time.Now().UTC().Format(time.RFC3339)
					middleware.ModelSuccessRate.WithLabelValues(modelID).Set(avgUptime)
					slog.Info("model performance metrics",
						"model", modelID,
						"success_rate", fmt.Sprintf("%.3f", avgUptime),
						"robots", len(uptimes),
					)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	const (
		s3BatchSize           = 100 // records per robot before S3 flush
		s3FlushInterval       = 30 * time.Second // time-based flush to reduce data loss window
		telemetryLogInterval  = 500 // log every N packets
		postgresThrottleRatio = 50  // upsert robot metadata every N packets
		batteryNominalVoltage = 48.0
	)

	var count int64
	robotPgCounts := make(map[string]int64)
	robotS3Counts := make(map[string]int64)
	s3Buffers := make(map[string][]byte) // robot_id → accumulated NDJSON
	lastFlush := time.Now()

	// flushS3Buffer writes accumulated NDJSON to S3 for a given robot.
	flushS3Buffer := func(robotID string) {
		buf := s3Buffers[robotID]
		if len(buf) == 0 {
			return
		}
		now := time.Now()
		s3Key := fmt.Sprintf("telemetry/%s/%s/batch_%d.ndjson",
			now.Format("2006/01/02/15"),
			robotID,
			now.UnixNano(),
		)
		if err := s3.Put(ctx, s3Key, buf, "application/x-ndjson"); err != nil {
			slog.Error("failed to flush telemetry batch to s3", "robot", robotID, "error", err)
		} else {
			slog.Debug("flushed telemetry batch", "robot", robotID, "records", robotS3Counts[robotID]%s3BatchSize, "key", s3Key)
		}
		s3Buffers[robotID] = nil
	}

	// sendToDLQ routes a failed message to the dead letter queue.
	sendToDLQ := func(key, value []byte, reason string) {
		if dlqProducer == nil {
			return
		}
		dlqMsg, _ := json.Marshal(map[string]any{
			"original_key":   string(key),
			"original_value": value,
			"reason":         reason,
			"timestamp":      time.Now().UTC(),
		})
		if err := dlqProducer.Publish(string(key), dlqMsg); err != nil {
			slog.Error("failed to send to DLQ", "error", err)
		}
	}

	err = consumer.Consume(ctx, func(key, value []byte) error {
		var pkt pb.TelemetryPacket
		if err := proto.Unmarshal(value, &pkt); err != nil {
			slog.Error("failed to unmarshal telemetry", "error", err)
			sendToDLQ(key, value, "unmarshal_error: "+err.Error())
			return nil // skip bad messages
		}

		count++
		middleware.TelemetryPacketsTotal.Inc()
		if count%telemetryLogInterval == 0 {
			slog.Info("processed telemetry", "count", count, "robot", pkt.RobotId)
		}

		// Process ALL payload types into S3 (FR1: video, LiDAR, audio)
		var ndjsonLine []byte
		switch payload := pkt.Payload.(type) {
		case *pb.TelemetryPacket_State:
			state := payload.State
			ndjsonLine, _ = json.Marshal(map[string]any{
				"type":     "state",
				"robot_id": state.RobotId,
				"status":   state.Status,
				"battery":  state.BatteryLevel,
				"x":        state.Pose.GetPosition().GetX(),
				"y":        state.Pose.GetPosition().GetY(),
				"z":        state.Pose.GetPosition().GetZ(),
				"ts":       time.Now().UnixMilli(),
			})
		case *pb.TelemetryPacket_Video:
			ndjsonLine, _ = json.Marshal(map[string]any{
				"type":     "video",
				"robot_id": pkt.RobotId,
				"encoding": payload.Video.Encoding,
				"width":    payload.Video.Width,
				"height":   payload.Video.Height,
				"size":     len(payload.Video.Data),
				"ts":       time.Now().UnixMilli(),
			})
			// Store raw video frame to S3 directly
			videoKey := fmt.Sprintf("video/%s/%s/%d.%s",
				time.Now().Format("2006/01/02/15"),
				pkt.RobotId, time.Now().UnixNano(),
				payload.Video.Encoding,
			)
			if err := s3.Put(ctx, videoKey, payload.Video.Data, "application/octet-stream"); err != nil {
				slog.Error("failed to store video frame", "robot", pkt.RobotId, "error", err)
			}
		case *pb.TelemetryPacket_Lidar:
			ndjsonLine, _ = json.Marshal(map[string]any{
				"type":       "lidar",
				"robot_id":   pkt.RobotId,
				"num_points": len(payload.Lidar.Points),
				"ts":         time.Now().UnixMilli(),
			})
			// Store raw LiDAR scan to S3
			lidarData, _ := proto.Marshal(payload.Lidar)
			lidarKey := fmt.Sprintf("lidar/%s/%s/%d.pb",
				time.Now().Format("2006/01/02/15"),
				pkt.RobotId, time.Now().UnixNano(),
			)
			if err := s3.Put(ctx, lidarKey, lidarData, "application/x-protobuf"); err != nil {
				slog.Error("failed to store lidar scan", "robot", pkt.RobotId, "error", err)
			}
		case *pb.TelemetryPacket_Audio:
			ndjsonLine, _ = json.Marshal(map[string]any{
				"type":        "audio",
				"robot_id":    pkt.RobotId,
				"sample_rate": payload.Audio.SampleRate,
				"channels":    payload.Audio.Channels,
				"size":        len(payload.Audio.Data),
				"ts":          time.Now().UnixMilli(),
			})
			// Store raw audio chunk to S3
			audioKey := fmt.Sprintf("audio/%s/%s/%d.pcm",
				time.Now().Format("2006/01/02/15"),
				pkt.RobotId, time.Now().UnixNano(),
			)
			if err := s3.Put(ctx, audioKey, payload.Audio.Data, "audio/pcm"); err != nil {
				slog.Error("failed to store audio chunk", "robot", pkt.RobotId, "error", err)
			}
		}

		// Buffer NDJSON metadata for all payload types
		if ndjsonLine != nil {
			s3Buffers[pkt.RobotId] = append(s3Buffers[pkt.RobotId], ndjsonLine...)
			s3Buffers[pkt.RobotId] = append(s3Buffers[pkt.RobotId], '\n')
			robotS3Counts[pkt.RobotId]++

			// Flush on batch size threshold
			if robotS3Counts[pkt.RobotId]%s3BatchSize == 0 {
				flushS3Buffer(pkt.RobotId)
			}
		}

		// Time-based flush: flush all buffers periodically to reduce data loss window
		if time.Since(lastFlush) > s3FlushInterval {
			for robotID := range s3Buffers {
				flushS3Buffer(robotID)
			}
			lastFlush = time.Now()
		}

		// Only update Redis hot state + Postgres for state packets
		state := pkt.GetState()
		if state == nil {
			return nil
		}

		now := time.Now()

		// Update Redis hot state with rich sensor data
		joints := make(map[string]float64, len(state.Joints))
		velocities := make(map[string]float64, len(state.Joints))
		torques := make(map[string]float64, len(state.Joints))
		for _, j := range state.Joints {
			joints[j.Name] = j.Position
			velocities[j.Name] = j.Velocity
			torques[j.Name] = j.Torque
		}
		hotState := &store.RobotHotState{
			RobotID:       state.RobotId,
			Status:        state.Status,
			PosX:          state.Pose.GetPosition().GetX(),
			PosY:          state.Pose.GetPosition().GetY(),
			PosZ:          state.Pose.GetPosition().GetZ(),
			BatteryLevel:  state.BatteryLevel,
			LastSeen:      now.Unix(),
			Joints:        joints,
			JointVelocity: velocities,
			JointTorque:   torques,
			BatteryV:      batteryNominalVoltage * state.BatteryLevel,
		}

		// Extract performance metrics from simulator
		if m := state.GetMetrics(); m != nil {
			hotState.Reward = m.Reward
			hotState.AvgReward = m.AvgEpisodeReward
			hotState.FallCount = int(m.FallCount)
			hotState.EpisodeCount = int(m.EpisodeCount)
			hotState.UptimePct = m.UptimePct
			hotState.ForwardVelocity = m.ForwardVelocity
		}
		if err := redis.SetRobotState(ctx, hotState); err != nil {
			slog.Error("failed to set redis state", "robot", state.RobotId, "error", err)
		}

		// Update per-robot Prometheus metrics
		middleware.RobotBatteryLevel.WithLabelValues(state.RobotId).Set(state.BatteryLevel)
		middleware.RobotLastSeenTimestamp.WithLabelValues(state.RobotId).Set(float64(now.Unix()))
		if m := state.GetMetrics(); m != nil {
			middleware.RobotReward.WithLabelValues(state.RobotId).Set(m.Reward)
			middleware.RobotUptimePct.WithLabelValues(state.RobotId).Set(m.UptimePct)
			middleware.RobotFallCount.WithLabelValues(state.RobotId).Set(float64(m.FallCount))
		}

		// Publish to Redis pub/sub for WebSocket fanout
		eventJSON, err := json.Marshal(hotState)
		if err != nil {
			slog.Error("failed to marshal hot state", "robot", state.RobotId, "error", err)
			return nil
		}
		_ = redis.PublishEvent(ctx, "telemetry:all", eventJSON)
		_ = redis.PublishEvent(ctx, "telemetry:"+state.RobotId, eventJSON)

		// Upsert robot to PostgreSQL (throttled — only every 50th update per robot)
		robotPgCounts[state.RobotId]++
		if robotPgCounts[state.RobotId]%postgresThrottleRatio == 0 {
			record := &store.RobotRecord{
				ID:           state.RobotId,
				Name:         state.RobotId,
				Model:        "humanoid-v1",
				Status:       state.Status,
				PosX:         state.Pose.GetPosition().GetX(),
				PosY:         state.Pose.GetPosition().GetY(),
				PosZ:         state.Pose.GetPosition().GetZ(),
				BatteryLevel: state.BatteryLevel,
				LastSeen:     now,
				RegisteredAt: now,
				TenantID:     "tenant-dev",
			}
			if err := pg.UpsertRobot(ctx, record); err != nil {
				slog.Error("failed to upsert robot", "robot", state.RobotId, "error", err)
			}
		}

		return nil
	})

	// Final flush: write any remaining buffered data to S3
	for robotID := range s3Buffers {
		flushS3Buffer(robotID)
	}

	if err != nil {
		slog.Error("consumer error", "error", err)
	}
	slog.Info("processor stopped", "total_processed", count)
}

func cmdInferenceInstruction(msg service.CommandMessage) string {
	if msg.InferenceRequest != nil {
		return msg.InferenceRequest.Instruction
	}
	return ""
}

func cmdInferenceModelID(msg service.CommandMessage) string {
	if msg.InferenceRequest != nil {
		return msg.InferenceRequest.ModelID
	}
	return ""
}
