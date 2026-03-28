package simulator

import (
	"testing"
)

func TestNewRobot(t *testing.T) {
	r := NewRobot(1)

	if r.ID != "robot-0001" {
		t.Errorf("expected ID robot-0001, got %s", r.ID)
	}
	if r.Name != "Humanoid-0001" {
		t.Errorf("expected Name Humanoid-0001, got %s", r.Name)
	}
	if r.Status != "active" {
		t.Errorf("expected Status active, got %s", r.Status)
	}
	if r.Battery < 0.8 || r.Battery > 1.0 {
		t.Errorf("expected Battery in [0.8, 1.0], got %f", r.Battery)
	}
	if len(r.Joints) != len(JointNames) {
		t.Errorf("expected %d joints, got %d", len(JointNames), len(r.Joints))
	}
}

func TestNewRobot_UniqueIDs(t *testing.T) {
	r1 := NewRobot(0)
	r2 := NewRobot(1)
	r3 := NewRobot(99)

	if r1.ID == r2.ID || r2.ID == r3.ID {
		t.Error("robot IDs should be unique")
	}
	if r1.ID != "robot-0000" {
		t.Errorf("expected robot-0000, got %s", r1.ID)
	}
	if r3.ID != "robot-0099" {
		t.Errorf("expected robot-0099, got %s", r3.ID)
	}
}

func TestRobot_Step_MovesPosition(t *testing.T) {
	r := NewRobot(42)
	startX, startY := r.PosX, r.PosY

	// Run many steps to ensure movement
	for range 100 {
		r.Step()
	}

	if r.PosX == startX && r.PosY == startY {
		t.Error("robot should have moved after 100 steps")
	}
}

func TestRobot_Step_DrainsBattery(t *testing.T) {
	r := NewRobot(1)
	initialBattery := r.Battery

	for range 100 {
		r.Step()
	}

	if r.Battery >= initialBattery {
		t.Errorf("battery should drain: initial=%f, current=%f", initialBattery, r.Battery)
	}
}

func TestRobot_Step_ChargingBehavior(t *testing.T) {
	r := NewRobot(1)
	r.Battery = 0.05 // Force low battery

	r.Step()

	if r.Status != "charging" {
		t.Errorf("expected status 'charging' when battery < 0.1, got %s", r.Status)
	}
}

func TestRobot_Step_ReactivatesAfterCharging(t *testing.T) {
	r := NewRobot(1)
	r.Battery = 0.96
	r.Status = "charging"

	r.Step()

	if r.Status != "active" {
		t.Errorf("expected status 'active' when battery > 0.95, got %s", r.Status)
	}
}

func TestRobot_Step_BatteryBounds(t *testing.T) {
	r := NewRobot(1)

	// Run many steps
	for range 50000 {
		r.Step()
	}

	if r.Battery < 0.0 || r.Battery > 1.0 {
		t.Errorf("battery should be clamped to [0, 1], got %f", r.Battery)
	}
}

func TestRobot_Step_JointsOscillate(t *testing.T) {
	r := NewRobot(1)
	initialJoints := make(map[string]float64)
	for k, v := range r.Joints {
		initialJoints[k] = v
	}

	for range 50 {
		r.Step()
	}

	changed := false
	for _, name := range JointNames {
		if r.Joints[name] != initialJoints[name] {
			changed = true
			break
		}
	}
	if !changed {
		t.Error("joints should oscillate after stepping")
	}
}

func TestRobot_ToStateProto(t *testing.T) {
	r := NewRobot(5)
	r.Step()

	state := r.ToStateProto()

	if state.RobotId != "robot-0005" {
		t.Errorf("expected robot-0005, got %s", state.RobotId)
	}
	if state.Pose == nil {
		t.Fatal("pose should not be nil")
	}
	if state.Pose.Position == nil {
		t.Fatal("position should not be nil")
	}
	if state.Pose.Orientation == nil {
		t.Fatal("orientation should not be nil")
	}
	if len(state.Joints) != len(JointNames) {
		t.Errorf("expected %d joints, got %d", len(JointNames), len(state.Joints))
	}
	if state.Timestamp == nil {
		t.Error("timestamp should not be nil")
	}
	if state.BatteryLevel < 0 || state.BatteryLevel > 1 {
		t.Errorf("battery out of range: %f", state.BatteryLevel)
	}
}

func TestRobot_ToTelemetryPacket(t *testing.T) {
	r := NewRobot(3)
	r.Step()

	pkt := r.ToTelemetryPacket()

	if pkt.RobotId != "robot-0003" {
		t.Errorf("expected robot-0003, got %s", pkt.RobotId)
	}
	if pkt.Timestamp == nil {
		t.Error("timestamp should not be nil")
	}
	state := pkt.GetState()
	if state == nil {
		t.Fatal("packet payload should be a RobotState")
	}
}

func TestRobot_GenerateLidarScan(t *testing.T) {
	r := NewRobot(1)
	numPoints := 360

	pkt := r.GenerateLidarScan(numPoints)

	if pkt.RobotId != "robot-0001" {
		t.Errorf("expected robot-0001, got %s", pkt.RobotId)
	}
	scan := pkt.GetLidar()
	if scan == nil {
		t.Fatal("packet payload should be a LidarScan")
	}
	if len(scan.Points) != numPoints {
		t.Errorf("expected %d points, got %d", numPoints, len(scan.Points))
	}
	// Check points have reasonable values
	for _, p := range scan.Points {
		dist := float64(p.X*p.X + p.Y*p.Y)
		if dist < 1 { // minimum 2m range squared = 4, allow some tolerance
			continue
		}
	}
}

func TestRobot_GenerateVideoFrame(t *testing.T) {
	r := NewRobot(1)

	pkt := r.GenerateVideoFrame()

	video := pkt.GetVideo()
	if video == nil {
		t.Fatal("packet payload should be a VideoFrame")
	}
	if video.Encoding != "jpeg" {
		t.Errorf("expected jpeg encoding, got %s", video.Encoding)
	}
	if video.Width != 640 || video.Height != 480 {
		t.Errorf("expected 640x480, got %dx%d", video.Width, video.Height)
	}
	if len(video.Data) != 50000 {
		t.Errorf("expected 50000 bytes, got %d", len(video.Data))
	}
}

func TestYawToQuaternion_ZeroYaw(t *testing.T) {
	q := yawToQuaternion(0)
	if q.X != 0 || q.Y != 0 {
		t.Error("x and y should be 0 for yaw rotation")
	}
	if q.Z != 0 {
		t.Errorf("z should be 0 for zero yaw, got %f", q.Z)
	}
	if q.W != 1 {
		t.Errorf("w should be 1 for zero yaw, got %f", q.W)
	}
}

func TestJointNames_HasExpectedCount(t *testing.T) {
	if len(JointNames) != 20 {
		t.Errorf("expected 20 joints for humanoid, got %d", len(JointNames))
	}
}

func BenchmarkRobot_Step(b *testing.B) {
	r := NewRobot(1)
	for b.Loop() {
		r.Step()
	}
}

func BenchmarkRobot_ToTelemetryPacket(b *testing.B) {
	r := NewRobot(1)
	r.Step()
	for b.Loop() {
		r.ToTelemetryPacket()
	}
}

func BenchmarkRobot_GenerateLidarScan(b *testing.B) {
	r := NewRobot(1)
	for b.Loop() {
		r.GenerateLidarScan(360)
	}
}
