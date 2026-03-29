package simulator

import "math"

// Action duration limits in seconds.
const (
	DanceDuration     = 15.0
	WaveDuration      = 5.0
	BowDuration       = 5.5
	LookAroundDuration = 8.0
	StretchDuration   = 8.0
	JumpDuration      = 11.0
	GravityAccel      = 9.81
)

// actionTime returns seconds since the current action started.
func (r *Robot) actionTime(dt float64) float64 {
	return float64(r.tick-r.ActionTick) * dt
}

// resetPose smoothly decays all joints toward zero (standing neutral).
func (r *Robot) resetPose(rate float64) {
	for _, name := range JointNames {
		r.Joints[name] *= rate
	}
}

// standing sets foot forces to even weight distribution.
func (r *Robot) standing() {
	w := HumanoidSpec.Mass * GravityAccel
	r.LeftFootForce = w / 2
	r.RightFootForce = w / 2
}

// stepDance — rhythmic full-body movement with arm raises and hip sway.
func (r *Robot) stepDance(dt float64) {
	t := r.actionTime(dt)
	bpm := 2.5 // beats per second
	p := t * bpm * 2 * math.Pi

	// Legs: alternating knee pumps
	r.Joints["left_hip_pitch"] = -0.2 * math.Sin(p)
	r.Joints["right_hip_pitch"] = 0.2 * math.Sin(p)
	r.Joints["left_knee"] = 0.3 + 0.3*math.Max(0, math.Sin(p))
	r.Joints["right_knee"] = 0.3 + 0.3*math.Max(0, -math.Sin(p))

	// Hip sway (lateral groove)
	r.Joints["left_hip_roll"] = 0.1 * math.Sin(p)
	r.Joints["right_hip_roll"] = -0.1 * math.Sin(p)

	// Arms: raised and pumping
	r.Joints["left_shoulder_pitch"] = -0.8 + 0.5*math.Sin(p*2)
	r.Joints["right_shoulder_pitch"] = -0.8 + 0.5*math.Sin(p*2+math.Pi)
	r.Joints["left_elbow"] = -1.2 + 0.4*math.Sin(p*2)
	r.Joints["right_elbow"] = -1.2 + 0.4*math.Sin(p*2+math.Pi)

	// Head: nodding to beat
	r.Joints["head_tilt"] = 0.1 * math.Sin(p*2)
	r.Joints["head_pan"] = 0.15 * math.Sin(p*0.5)

	// Ankle push
	r.Joints["left_ankle_pitch"] = -0.1 * math.Sin(p)
	r.Joints["right_ankle_pitch"] = 0.1 * math.Sin(p)

	// Foot forces: bouncy
	w := HumanoidSpec.Mass * GravityAccel
	r.LeftFootForce = w * (0.5 + 0.3*math.Sin(p))
	r.RightFootForce = w * (0.5 - 0.3*math.Sin(p))

	// Slight body rotation (grooving)
	r.Yaw += math.Sin(p*0.25) * 0.005

	// Auto-stop after 15 seconds
	if t > DanceDuration {
		r.Action = ""
	}
}

// stepWave — raise right arm and wave hand back and forth.
func (r *Robot) stepWave(dt float64) {
	t := r.actionTime(dt)
	p := t * 3 * 2 * math.Pi // wave frequency

	// Standing pose for everything except right arm
	r.resetPose(0.85)

	// Right arm: raised and waving
	r.Joints["right_shoulder_pitch"] = -2.2
	r.Joints["right_shoulder_roll"] = -0.3
	r.Joints["right_elbow"] = -0.8 + 0.5*math.Sin(p)

	// Slight head tilt toward waving direction
	r.Joints["head_pan"] = -0.15
	r.Joints["head_tilt"] = 0.05 * math.Sin(p*0.5)

	// Left arm relaxed
	r.Joints["left_shoulder_pitch"] = 0.1
	r.Joints["left_elbow"] = -0.2

	r.standing()

	if t > WaveDuration {
		r.Action = ""
	}
}

// stepBow — lean forward at the waist.
func (r *Robot) stepBow(dt float64) {
	t := r.actionTime(dt)

	r.resetPose(0.85)

	// Bow phases: lean forward (0-2s), hold (2-4s), come back (4-5s)
	var bowAngle float64
	if t < 2 {
		bowAngle = -0.6 * (t / 2) // lean forward
	} else if t < 4 {
		bowAngle = -0.6 // hold
	} else if t < 5 {
		bowAngle = -0.6 * (1 - (t-4)/1) // come back
	}

	r.Joints["left_hip_pitch"] = bowAngle
	r.Joints["right_hip_pitch"] = bowAngle
	// Knees slightly bent to compensate
	r.Joints["left_knee"] = -bowAngle * 0.3
	r.Joints["right_knee"] = -bowAngle * 0.3
	// Arms at sides, slightly back
	r.Joints["left_shoulder_pitch"] = -bowAngle * 0.4
	r.Joints["right_shoulder_pitch"] = -bowAngle * 0.4
	r.Joints["left_elbow"] = -0.1
	r.Joints["right_elbow"] = -0.1
	// Head down
	r.Joints["head_tilt"] = bowAngle * 0.3

	r.standing()

	if t > BowDuration {
		r.Action = ""
	}
}

// stepSit — lower into a seated/crouching position.
func (r *Robot) stepSit(dt float64) {
	t := r.actionTime(dt)

	// Transition to sitting over 2 seconds, hold
	progress := math.Min(1, t/2)

	r.Joints["left_hip_pitch"] = -1.2 * progress
	r.Joints["right_hip_pitch"] = -1.2 * progress
	r.Joints["left_knee"] = 2.0 * progress
	r.Joints["right_knee"] = 2.0 * progress
	r.Joints["left_ankle_pitch"] = -0.4 * progress
	r.Joints["right_ankle_pitch"] = -0.4 * progress

	// Arms resting on knees
	r.Joints["left_shoulder_pitch"] = -0.3 * progress
	r.Joints["right_shoulder_pitch"] = -0.3 * progress
	r.Joints["left_elbow"] = -1.0 * progress
	r.Joints["right_elbow"] = -1.0 * progress

	// Head looking forward
	r.Joints["head_tilt"] = 0.1 * progress

	r.standing()

	// Stay seated until a new command
}

// stepJump — crouch and spring up repeatedly.
func (r *Robot) stepJump(dt float64) {
	t := r.actionTime(dt)
	// Jump cycle: 0.8s per jump
	cycle := math.Mod(t, 0.8)

	if cycle < 0.3 {
		// Crouch phase
		crouch := cycle / 0.3
		r.Joints["left_hip_pitch"] = -0.5 * crouch
		r.Joints["right_hip_pitch"] = -0.5 * crouch
		r.Joints["left_knee"] = 1.0 * crouch
		r.Joints["right_knee"] = 1.0 * crouch
		r.Joints["left_shoulder_pitch"] = 0.3 * crouch
		r.Joints["right_shoulder_pitch"] = 0.3 * crouch
		r.Joints["left_elbow"] = -0.5
		r.Joints["right_elbow"] = -0.5
		r.LeftFootForce = HumanoidSpec.Mass * GravityAccel * (0.5 + 0.5*crouch)
		r.RightFootForce = r.LeftFootForce
	} else if cycle < 0.5 {
		// Spring up — extend everything
		extend := (cycle - 0.3) / 0.2
		r.Joints["left_hip_pitch"] = -0.5 * (1 - extend)
		r.Joints["right_hip_pitch"] = -0.5 * (1 - extend)
		r.Joints["left_knee"] = 1.0 * (1 - extend)
		r.Joints["right_knee"] = 1.0 * (1 - extend)
		r.Joints["left_shoulder_pitch"] = -1.5 * extend // arms up
		r.Joints["right_shoulder_pitch"] = -1.5 * extend
		r.Joints["left_elbow"] = -0.2
		r.Joints["right_elbow"] = -0.2
		// Airborne: no foot force
		r.LeftFootForce = HumanoidSpec.Mass * GravityAccel * math.Max(0, 1-extend*3)
		r.RightFootForce = r.LeftFootForce
		r.PosZ = 0.15 * extend // hop height
	} else {
		// Landing
		land := (cycle - 0.5) / 0.3
		r.Joints["left_hip_pitch"] = -0.2 * (1 - land)
		r.Joints["right_hip_pitch"] = -0.2 * (1 - land)
		r.Joints["left_knee"] = 0.3 * (1 - land)
		r.Joints["right_knee"] = 0.3 * (1 - land)
		r.Joints["left_shoulder_pitch"] = -1.5 * (1 - land)
		r.Joints["right_shoulder_pitch"] = -1.5 * (1 - land)
		r.Joints["left_elbow"] = -0.3
		r.Joints["right_elbow"] = -0.3
		r.LeftFootForce = HumanoidSpec.Mass * GravityAccel * (0.5 + 0.5*land)
		r.RightFootForce = r.LeftFootForce
		r.PosZ = 0.15 * (1 - land)
	}

	r.Joints["head_tilt"] = r.Joints["left_hip_pitch"] * 0.2

	if t > LookAroundDuration {
		r.Action = ""
		r.PosZ = 0
	}
}

// stepLookAround — stand still, rotate head scanning the environment.
func (r *Robot) stepLookAround(dt float64) {
	t := r.actionTime(dt)

	r.resetPose(0.85)

	// Head scans: pan wide, tilt up/down
	r.Joints["head_pan"] = 1.0 * math.Sin(t*0.8)
	r.Joints["head_tilt"] = 0.3 * math.Sin(t*1.2+1)

	// Body turns slightly with head
	r.Joints["left_hip_yaw"] = 0.05 * math.Sin(t*0.8)
	r.Joints["right_hip_yaw"] = -0.05 * math.Sin(t*0.8)

	// One hand shielding eyes
	r.Joints["right_shoulder_pitch"] = -1.0
	r.Joints["right_elbow"] = -1.5
	r.Joints["left_shoulder_pitch"] = 0.1
	r.Joints["left_elbow"] = -0.15

	r.standing()

	if t > StretchDuration {
		r.Action = ""
	}
}

// stepStretch — arms up overhead stretch, then side bends.
func (r *Robot) stepStretch(dt float64) {
	t := r.actionTime(dt)

	r.resetPose(0.85)

	if t < 4 {
		// Phase 1: arms overhead stretch
		progress := math.Min(1, t/1.5)
		r.Joints["left_shoulder_pitch"] = -3.0 * progress
		r.Joints["right_shoulder_pitch"] = -3.0 * progress
		r.Joints["left_elbow"] = -0.1
		r.Joints["right_elbow"] = -0.1
		// Slight back bend
		r.Joints["left_hip_pitch"] = 0.1 * progress
		r.Joints["right_hip_pitch"] = 0.1 * progress
		r.Joints["head_tilt"] = -0.2 * progress // look up
	} else if t < 8 {
		// Phase 2: side bends
		bend := math.Sin((t - 4) * 1.5)
		r.Joints["left_shoulder_pitch"] = -2.5
		r.Joints["right_shoulder_pitch"] = -2.5
		r.Joints["left_hip_roll"] = 0.2 * bend
		r.Joints["right_hip_roll"] = 0.2 * bend
		r.Joints["head_pan"] = 0.1 * bend
	} else if t < 10 {
		// Phase 3: forward bend / touch toes
		progress := math.Min(1, (t-8)/1.5)
		r.Joints["left_hip_pitch"] = -1.0 * progress
		r.Joints["right_hip_pitch"] = -1.0 * progress
		r.Joints["left_shoulder_pitch"] = 0.3
		r.Joints["right_shoulder_pitch"] = 0.3
		r.Joints["left_knee"] = 0.1
		r.Joints["right_knee"] = 0.1
		r.Joints["head_tilt"] = 0.3 * progress
	}

	r.standing()

	if t > JumpDuration {
		r.Action = ""
	}
}
