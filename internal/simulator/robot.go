package simulator

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	pb "github.com/dimuthu/robot-fleet/internal/telemetry"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// JointNames for a humanoid robot (matching Unitree G1 / GR1 layout)
var JointNames = []string{
	"head_pan", "head_tilt",
	"left_shoulder_pitch", "left_shoulder_roll", "left_elbow",
	"right_shoulder_pitch", "right_shoulder_roll", "right_elbow",
	"left_hip_yaw", "left_hip_roll", "left_hip_pitch", "left_knee", "left_ankle_pitch", "left_ankle_roll",
	"right_hip_yaw", "right_hip_roll", "right_hip_pitch", "right_knee", "right_ankle_pitch", "right_ankle_roll",
}

// JointLimits defines realistic joint angle limits in radians.
var JointLimits = map[string][2]float64{
	"head_pan": {-1.57, 1.57}, "head_tilt": {-0.78, 0.78},
	"left_shoulder_pitch": {-3.14, 1.57}, "left_shoulder_roll": {-0.26, 3.14}, "left_elbow": {-2.36, 0.0},
	"right_shoulder_pitch": {-3.14, 1.57}, "right_shoulder_roll": {-3.14, 0.26}, "right_elbow": {-2.36, 0.0},
	"left_hip_yaw": {-0.43, 0.43}, "left_hip_roll": {-0.26, 0.52}, "left_hip_pitch": {-1.57, 0.52},
	"left_knee": {0.0, 2.09}, "left_ankle_pitch": {-0.87, 0.52}, "left_ankle_roll": {-0.26, 0.26},
	"right_hip_yaw": {-0.43, 0.43}, "right_hip_roll": {-0.52, 0.26}, "right_hip_pitch": {-1.57, 0.52},
	"right_knee": {0.0, 2.09}, "right_ankle_pitch": {-0.87, 0.52}, "right_ankle_roll": {-0.26, 0.26},
}

// HumanoidSpec defines the physical characteristics of the humanoid robot.
var HumanoidSpec = struct {
	Height      float64 // meters
	Mass        float64 // kg
	FootLength  float64 // meters
	StepLength  float64 // meters
	WalkSpeed   float64 // m/s
	BatteryWh   float64 // watt-hours
	BatteryV    float64 // voltage
	CPUTempIdle float64 // celsius
	CPUTempLoad float64 // celsius
}{
	Height: 1.65, Mass: 55.0, FootLength: 0.25,
	StepLength: 0.4, WalkSpeed: 1.2,
	BatteryWh: 864.0, BatteryV: 48.0,
	CPUTempIdle: 42.0, CPUTempLoad: 72.0,
}

// Physics constants for the humanoid simulation.
const (
	SimTickDuration    = 0.1     // seconds per simulation tick
	BatteryIdleDrain   = 0.00002 // battery % per tick when idle
	BatteryActiveDrain = 0.00008 // battery % per tick when walking/active
	ThermalDecayRate   = 0.01    // rate at which CPU temp approaches target
	GaitFrequencyHz    = 1.8     // human walking cadence in Hz
	ChargingRate       = 0.0005  // battery % recovered per tick
	LowBatteryThreshold = 0.15  // battery level that triggers charging
)

// Robot simulates a single humanoid robot with realistic sensor output.
type Robot struct {
	ID   string
	Name string

	// State
	Status  string
	Battery float64
	PosX    float64
	PosY    float64
	PosZ    float64
	Yaw     float64

	// Joints: position, velocity, torque (computed each step)
	Joints         map[string]float64
	JointVelocity  map[string]float64
	JointTorque    map[string]float64

	// IMU sensor data
	LinearAccelX float64 // m/s^2
	LinearAccelY float64
	LinearAccelZ float64
	AngularVelX  float64 // rad/s
	AngularVelY  float64
	AngularVelZ  float64

	// Odometry
	OdomVelX   float64 // m/s
	OdomVelY   float64
	OdomVelYaw float64 // rad/s
	DistanceTotal float64 // total meters traveled

	// Foot force sensors (Newtons)
	LeftFootForce  float64
	RightFootForce float64

	// Internals
	CPUTemp     float64 // Celsius
	MotorTemp   float64
	WiFiRSSI    int     // dBm
	UptimeSecs  int64

	// Command state
	Stopped   bool
	TargetX   float64
	TargetY   float64
	HasTarget bool
	Action    string // "" = idle, "walk", "dance", "wave", "bow", "sit", "jump", "look_around"
	ActionTick int64 // tick when action started

	// Physics state
	prevPosX float64
	prevPosY float64
	prevYaw  float64
	tick     int64
	rng      *rand.Rand
	startTime time.Time
}

func NewRobot(id int) *Robot {
	r := &Robot{
		ID:            fmt.Sprintf("robot-%04d", id),
		Name:          fmt.Sprintf("Humanoid-%04d", id),
		Status:        "active",
		Battery:       0.8 + rand.Float64()*0.2,
		PosX:          rand.Float64()*60 - 30,
		PosY:          rand.Float64()*60 - 30,
		PosZ:          0.0,
		Yaw:           rand.Float64() * 2 * math.Pi,
		Joints:        make(map[string]float64),
		JointVelocity: make(map[string]float64),
		JointTorque:   make(map[string]float64),
		CPUTemp:       HumanoidSpec.CPUTempIdle + rand.Float64()*5,
		MotorTemp:     35 + rand.Float64()*5,
		WiFiRSSI:      -40 - int(rand.Int64N(30)),
		rng:           rand.New(rand.NewPCG(uint64(id), uint64(time.Now().UnixNano()))),
		startTime:     time.Now(),
	}
	for _, name := range JointNames {
		r.Joints[name] = (r.rng.Float64() - 0.5) * 0.2
		r.JointVelocity[name] = 0
		r.JointTorque[name] = 0
	}
	return r
}

// Step advances the simulation by one tick with realistic physics.
func (r *Robot) Step() {
	r.tick++
	r.UptimeSecs = int64(time.Since(r.startTime).Seconds())
	dt := SimTickDuration

	r.prevPosX = r.PosX
	r.prevPosY = r.PosY
	r.prevYaw = r.Yaw

	if r.Stopped {
		// Joints decay to rest position
		for _, name := range JointNames {
			r.JointVelocity[name] = -r.Joints[name] * 2.0
			r.Joints[name] *= 0.9
			r.JointTorque[name] = r.JointVelocity[name] * 5.0
		}
		r.LeftFootForce = HumanoidSpec.Mass * GravityAccel / 2
		r.RightFootForce = HumanoidSpec.Mass * GravityAccel / 2
	} else if r.Action == "dance" {
		r.stepDance(dt)
	} else if r.Action == "wave" {
		r.stepWave(dt)
	} else if r.Action == "bow" {
		r.stepBow(dt)
	} else if r.Action == "sit" {
		r.stepSit(dt)
	} else if r.Action == "jump" {
		r.stepJump(dt)
	} else if r.Action == "look_around" {
		r.stepLookAround(dt)
	} else if r.Action == "stretch" {
		r.stepStretch(dt)
	} else if r.HasTarget {
		// Full walking gait — only when actively moving to a target
		t := float64(r.tick) * dt
		gaitFreq := GaitFrequencyHz
		gaitPhase := t * gaitFreq * 2 * math.Pi

		// Hip pitch: main walking driver (opposite phase left/right)
		r.Joints["left_hip_pitch"] = -0.4 * math.Sin(gaitPhase)
		r.Joints["right_hip_pitch"] = 0.4 * math.Sin(gaitPhase)

		// Knee: always positive, flex on swing phase
		r.Joints["left_knee"] = 0.6 + 0.4*math.Max(0, math.Sin(gaitPhase))
		r.Joints["right_knee"] = 0.6 + 0.4*math.Max(0, -math.Sin(gaitPhase))

		// Ankle pitch: push-off and toe clearance
		r.Joints["left_ankle_pitch"] = -0.15 * math.Sin(gaitPhase+0.5)
		r.Joints["right_ankle_pitch"] = 0.15 * math.Sin(gaitPhase+0.5)

		// Hip roll: lateral sway
		r.Joints["left_hip_roll"] = 0.05 * math.Sin(gaitPhase*0.5)
		r.Joints["right_hip_roll"] = -0.05 * math.Sin(gaitPhase*0.5)

		// Arms swing opposite to legs
		r.Joints["left_shoulder_pitch"] = 0.3 * math.Sin(gaitPhase)
		r.Joints["right_shoulder_pitch"] = -0.3 * math.Sin(gaitPhase)
		r.Joints["left_elbow"] = -0.4 - 0.2*math.Abs(math.Sin(gaitPhase))
		r.Joints["right_elbow"] = -0.4 - 0.2*math.Abs(math.Sin(gaitPhase))

		// Head slight bob
		r.Joints["head_tilt"] = 0.02 * math.Sin(gaitPhase*2)

		// Foot forces: alternating weight shift
		totalWeight := HumanoidSpec.Mass * GravityAccel
		leftPhase := 0.5 + 0.5*math.Sin(gaitPhase)
		r.LeftFootForce = totalWeight * leftPhase
		r.RightFootForce = totalWeight * (1 - leftPhase)

		// Position: move toward target
		dx := r.TargetX - r.PosX
		dy := r.TargetY - r.PosY
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < 0.2 {
			r.HasTarget = false
		} else {
			speed := math.Min(HumanoidSpec.WalkSpeed*dt, dist)
			r.PosX += (dx / dist) * speed
			r.PosY += (dy / dist) * speed
			r.Yaw = math.Atan2(dy, dx)
		}
	} else {
		// Idle standing — subtle breathing/sway, no walking gait
		t := float64(r.tick) * dt
		breathPhase := t * 0.3 * 2 * math.Pi // slow breathing ~0.3 Hz

		// Standing pose: straight legs, arms at sides, slight sway
		r.Joints["left_hip_pitch"] *= 0.9  // decay to zero
		r.Joints["right_hip_pitch"] *= 0.9
		r.Joints["left_knee"] = r.Joints["left_knee"]*0.9 + 0.05*0.1 // very slight bend
		r.Joints["right_knee"] = r.Joints["right_knee"]*0.9 + 0.05*0.1
		r.Joints["left_ankle_pitch"] *= 0.9
		r.Joints["right_ankle_pitch"] *= 0.9

		// Arms relaxed at sides with tiny sway
		r.Joints["left_shoulder_pitch"] = r.Joints["left_shoulder_pitch"]*0.9 + 0.02*math.Sin(breathPhase)*0.1
		r.Joints["right_shoulder_pitch"] = r.Joints["right_shoulder_pitch"]*0.9 + 0.02*math.Sin(breathPhase)*0.1
		r.Joints["left_elbow"] = r.Joints["left_elbow"]*0.9 + (-0.15)*0.1 // slight natural bend
		r.Joints["right_elbow"] = r.Joints["right_elbow"]*0.9 + (-0.15)*0.1

		// Subtle body sway (breathing)
		r.Joints["left_hip_roll"] = 0.01 * math.Sin(breathPhase)
		r.Joints["right_hip_roll"] = -0.01 * math.Sin(breathPhase)

		// Head looks around slowly
		r.Joints["head_pan"] = 0.15 * math.Sin(t*0.15*2*math.Pi)
		r.Joints["head_tilt"] = 0.03 * math.Sin(breathPhase*1.3)

		// Standing evenly on both feet
		totalWeight := HumanoidSpec.Mass * GravityAccel
		r.LeftFootForce = totalWeight/2 + totalWeight*0.02*math.Sin(breathPhase)
		r.RightFootForce = totalWeight/2 - totalWeight*0.02*math.Sin(breathPhase)

		// Slow random drift (not walking, just small position shifts)
		r.PosX += (r.rng.Float64() - 0.5) * 0.02
		r.PosY += (r.rng.Float64() - 0.5) * 0.02
		r.Yaw += (r.rng.Float64() - 0.5) * 0.01
	}

	// Compute joint velocities and torques for all modes
	{
		for _, name := range JointNames {
			prevPos := r.Joints[name]
			if lim, ok := JointLimits[name]; ok {
				r.Joints[name] = math.Max(lim[0], math.Min(lim[1], r.Joints[name]))
			}
			r.JointVelocity[name] = (r.Joints[name] - prevPos) / dt
			r.JointTorque[name] = r.JointVelocity[name]*2.0 + r.rng.Float64()*0.5
		}
	}

	// Odometry
	dx := r.PosX - r.prevPosX
	dy := r.PosY - r.prevPosY
	r.OdomVelX = dx / dt
	r.OdomVelY = dy / dt
	r.OdomVelYaw = (r.Yaw - r.prevYaw) / dt
	r.DistanceTotal += math.Sqrt(dx*dx + dy*dy)

	// IMU: accelerometer (gravity + motion) and gyroscope
	r.LinearAccelX = r.OdomVelX/dt + r.rng.Float64()*0.1 // noise
	r.LinearAccelY = r.OdomVelY/dt + r.rng.Float64()*0.1
	r.LinearAccelZ = GravityAccel + r.rng.Float64()*0.05 // gravity + vibration
	r.AngularVelX = r.rng.Float64() * 0.02  // slight body sway
	r.AngularVelY = r.rng.Float64() * 0.02
	r.AngularVelZ = r.OdomVelYaw

	// Battery: drain based on activity
	if r.Stopped {
		r.Battery -= BatteryIdleDrain
	} else {
		r.Battery -= BatteryActiveDrain
	}
	if r.Battery < 0.1 {
		r.Status = "charging"
		r.Battery += 0.0005
	}
	if r.Battery > 0.95 && !r.Stopped {
		r.Status = "active"
	}
	r.Battery = math.Min(1.0, math.Max(0.0, r.Battery))

	// Thermal model
	targetTemp := HumanoidSpec.CPUTempIdle
	if !r.Stopped {
		targetTemp = HumanoidSpec.CPUTempLoad
	}
	r.CPUTemp += (targetTemp - r.CPUTemp) * ThermalDecayRate
	r.MotorTemp += (targetTemp - 5 - r.MotorTemp) * 0.005
	r.CPUTemp += (r.rng.Float64() - 0.5) * 0.2

	// WiFi signal fluctuation
	r.WiFiRSSI = -40 - int(r.rng.Int64N(30)) + int(math.Sin(float64(r.tick)*0.01)*5)

	// Add sensor noise to joints
	for _, name := range JointNames {
		r.Joints[name] += (r.rng.Float64() - 0.5) * 0.001 // encoder noise
	}
}

// ApplyCommand applies a command to the robot.
func (r *Robot) ApplyCommand(cmdType string, params map[string]any) {
	switch cmdType {
	case "move":
		if x, ok := params["x"].(float64); ok {
			r.TargetX = x
		}
		if y, ok := params["y"].(float64); ok {
			r.TargetY = y
		}
		r.HasTarget = true
		r.Stopped = false
		r.Action = "walk"
		r.ActionTick = r.tick
		if r.Status != "charging" {
			r.Status = "active"
		}
	case "stop":
		emergency, _ := params["emergency"].(bool)
		r.Stopped = true
		r.HasTarget = false
		r.Action = ""
		if emergency {
			r.Status = "error"
		} else {
			r.Status = "idle"
		}
	case "dance", "wave", "bow", "sit", "jump", "look_around", "stretch":
		r.Stopped = false
		r.HasTarget = false
		r.Action = cmdType
		r.ActionTick = r.tick
		if r.Status != "charging" {
			r.Status = "active"
		}
	case "semantic":
		// Semantic commands carry an "instruction" string that the API already
		// interpreted into a type. If it arrives here as "semantic", parse the
		// instruction for action keywords.
		if instruction, ok := params["instruction"].(string); ok {
			r.applySemanticAction(instruction)
		}
	}
}

func (r *Robot) applySemanticAction(instruction string) {
	r.Stopped = false
	r.HasTarget = false
	r.ActionTick = r.tick
	if r.Status != "charging" {
		r.Status = "active"
	}

	// Match keywords to actions
	lower := instruction
	switch {
	case contains(lower, "dance"):
		r.Action = "dance"
	case contains(lower, "wave"), contains(lower, "hello"), contains(lower, "hi"), contains(lower, "greet"):
		r.Action = "wave"
	case contains(lower, "bow"), contains(lower, "respect"):
		r.Action = "bow"
	case contains(lower, "sit"), contains(lower, "crouch"):
		r.Action = "sit"
	case contains(lower, "jump"), contains(lower, "hop"):
		r.Action = "jump"
	case contains(lower, "look"), contains(lower, "scan"), contains(lower, "search"):
		r.Action = "look_around"
	case contains(lower, "stretch"), contains(lower, "warm up"):
		r.Action = "stretch"
	case contains(lower, "stop"), contains(lower, "halt"):
		r.Action = ""
		r.Stopped = true
		r.Status = "idle"
	default:
		r.Action = "wave" // default: acknowledge with a wave
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ToStateProto returns the current robot state as a protobuf message.
func (r *Robot) ToStateProto() *pb.RobotState {
	joints := make([]*pb.JointState, 0, len(JointNames))
	for _, name := range JointNames {
		joints = append(joints, &pb.JointState{
			Name:     name,
			Position: r.Joints[name],
			Velocity: r.JointVelocity[name],
			Torque:   r.JointTorque[name],
		})
	}

	return &pb.RobotState{
		RobotId: r.ID,
		Pose: &pb.Pose{
			Position:    &pb.Vector3{X: r.PosX, Y: r.PosY, Z: r.PosZ},
			Orientation: yawToQuaternion(r.Yaw),
		},
		Joints:       joints,
		BatteryLevel: r.Battery,
		Status:       r.Status,
		Timestamp:    timestamppb.Now(),
	}
}

// ToTelemetryPacket wraps the state in a TelemetryPacket.
func (r *Robot) ToTelemetryPacket() *pb.TelemetryPacket {
	return &pb.TelemetryPacket{
		RobotId:   r.ID,
		Timestamp: timestamppb.Now(),
		Payload:   &pb.TelemetryPacket_State{State: r.ToStateProto()},
	}
}

// GenerateLidarScan produces a simulated 2D LiDAR scan with realistic noise.
func (r *Robot) GenerateLidarScan(numPoints int) *pb.TelemetryPacket {
	points := make([]*pb.LidarPoint, numPoints)
	for i := range numPoints {
		angle := float64(i)/float64(numPoints)*2*math.Pi + r.Yaw
		// Simulate environment: walls at ~8-12m with some closer objects
		baseDist := 8.0 + 4.0*math.Sin(angle*3) // irregular room shape
		if r.rng.Float64() < 0.1 {
			baseDist = 1.5 + r.rng.Float64()*3.0 // occasional obstacle
		}
		dist := baseDist + r.rng.Float64()*0.05 // range noise
		points[i] = &pb.LidarPoint{
			X:         float32(dist * math.Cos(angle)),
			Y:         float32(dist * math.Sin(angle)),
			Z:         float32(0.3 + r.rng.Float32()*0.02),
			Intensity: float32(0.5 + r.rng.Float32()*0.5),
		}
	}
	return &pb.TelemetryPacket{
		RobotId:   r.ID,
		Timestamp: timestamppb.Now(),
		Payload: &pb.TelemetryPacket_Lidar{
			Lidar: &pb.LidarScan{
				Points:    points,
				Timestamp: timestamppb.Now(),
			},
		},
	}
}

// GenerateVideoFrame produces a simulated video frame (fake JPEG bytes).
func (r *Robot) GenerateVideoFrame() *pb.TelemetryPacket {
	fakeJPEG := make([]byte, 50000)
	for i := range fakeJPEG {
		fakeJPEG[i] = byte(r.rng.IntN(256))
	}
	return &pb.TelemetryPacket{
		RobotId:   r.ID,
		Timestamp: timestamppb.Now(),
		Payload: &pb.TelemetryPacket_Video{
			Video: &pb.VideoFrame{
				Data:      fakeJPEG,
				Encoding:  "jpeg",
				Width:     640,
				Height:    480,
				Timestamp: timestamppb.Now(),
			},
		},
	}
}

// SensorSnapshot returns all sensor data for Redis/ROS bridge publishing.
type SensorSnapshot struct {
	RobotID        string             `json:"robot_id"`
	Status         string             `json:"status"`
	PosX           float64            `json:"pos_x"`
	PosY           float64            `json:"pos_y"`
	PosZ           float64            `json:"pos_z"`
	Yaw            float64            `json:"yaw"`
	BatteryLevel   float64            `json:"battery_level"`
	BatteryVoltage float64            `json:"battery_voltage"`
	Joints         map[string]float64 `json:"joints"`
	JointVelocity  map[string]float64 `json:"joint_velocities"`
	JointTorque    map[string]float64 `json:"joint_torques"`
	IMU            IMUData            `json:"imu"`
	Odometry       OdomData           `json:"odometry"`
	FootForce      FootForceData      `json:"foot_force"`
	CPUTemp        float64            `json:"cpu_temp"`
	MotorTemp      float64            `json:"motor_temp"`
	WiFiRSSI       int                `json:"wifi_rssi"`
	UptimeSecs     int64              `json:"uptime_secs"`
	DistanceTotal  float64            `json:"distance_total"`
}

type IMUData struct {
	LinearAccelX float64 `json:"linear_accel_x"`
	LinearAccelY float64 `json:"linear_accel_y"`
	LinearAccelZ float64 `json:"linear_accel_z"`
	AngularVelX  float64 `json:"angular_vel_x"`
	AngularVelY  float64 `json:"angular_vel_y"`
	AngularVelZ  float64 `json:"angular_vel_z"`
}

type OdomData struct {
	VelX   float64 `json:"vel_x"`
	VelY   float64 `json:"vel_y"`
	VelYaw float64 `json:"vel_yaw"`
}

type FootForceData struct {
	Left  float64 `json:"left"`
	Right float64 `json:"right"`
}

// Snapshot returns a complete sensor snapshot for publishing.
func (r *Robot) Snapshot() *SensorSnapshot {
	joints := make(map[string]float64, len(JointNames))
	velocities := make(map[string]float64, len(JointNames))
	torques := make(map[string]float64, len(JointNames))
	for _, name := range JointNames {
		joints[name] = r.Joints[name]
		velocities[name] = r.JointVelocity[name]
		torques[name] = r.JointTorque[name]
	}
	return &SensorSnapshot{
		RobotID: r.ID, Status: r.Status,
		PosX: r.PosX, PosY: r.PosY, PosZ: r.PosZ, Yaw: r.Yaw,
		BatteryLevel: r.Battery, BatteryVoltage: HumanoidSpec.BatteryV * r.Battery,
		Joints: joints, JointVelocity: velocities, JointTorque: torques,
		IMU: IMUData{r.LinearAccelX, r.LinearAccelY, r.LinearAccelZ, r.AngularVelX, r.AngularVelY, r.AngularVelZ},
		Odometry:  OdomData{r.OdomVelX, r.OdomVelY, r.OdomVelYaw},
		FootForce: FootForceData{r.LeftFootForce, r.RightFootForce},
		CPUTemp: r.CPUTemp, MotorTemp: r.MotorTemp,
		WiFiRSSI: r.WiFiRSSI, UptimeSecs: r.UptimeSecs,
		DistanceTotal: r.DistanceTotal,
	}
}

func yawToQuaternion(yaw float64) *pb.Quaternion {
	return &pb.Quaternion{
		X: 0, Y: 0,
		Z: math.Sin(yaw / 2),
		W: math.Cos(yaw / 2),
	}
}
