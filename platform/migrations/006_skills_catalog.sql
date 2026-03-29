-- Motor skills catalog

CREATE TABLE IF NOT EXISTS skills_catalog (
    id              VARCHAR(64) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    skill_type      VARCHAR(64) NOT NULL DEFAULT 'locomotion',
    required_joints JSONB DEFAULT '[]',
    version         VARCHAR(64) NOT NULL DEFAULT '1.0.0',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed with default motor skills
INSERT INTO skills_catalog (id, name, description, skill_type, required_joints, version) VALUES
    ('bipedal-walk', 'Bipedal Walk', 'Standard bipedal walking locomotion', 'locomotion', '["l_hip_pitch","r_hip_pitch","l_knee","r_knee","l_ankle_pitch","r_ankle_pitch"]', '1.0.0'),
    ('bipedal-run', 'Bipedal Run', 'Running locomotion with flight phase', 'locomotion', '["l_hip_pitch","r_hip_pitch","l_knee","r_knee","l_ankle_pitch","r_ankle_pitch"]', '1.0.0'),
    ('precision-grasp', 'Precision Grasp', 'Fine-grained object grasping with force control', 'manipulation', '["l_shoulder_pitch","l_elbow","l_wrist","r_shoulder_pitch","r_elbow","r_wrist"]', '1.0.0'),
    ('push-recovery', 'Push Recovery', 'Balance recovery from external perturbations', 'locomotion', '["l_hip_pitch","r_hip_pitch","l_hip_roll","r_hip_roll","torso_yaw"]', '1.0.0'),
    ('stair-climb', 'Stair Climbing', 'Ascending and descending stairs', 'locomotion', '["l_hip_pitch","r_hip_pitch","l_knee","r_knee","l_ankle_pitch","r_ankle_pitch"]', '0.8.0'),
    ('object-place', 'Object Placement', 'Place objects at target locations with precision', 'manipulation', '["l_shoulder_pitch","l_shoulder_roll","l_elbow","l_wrist"]', '1.0.0'),
    ('wave-gesture', 'Wave Gesture', 'Social waving gesture', 'social', '["r_shoulder_pitch","r_shoulder_roll","r_elbow"]', '1.0.0'),
    ('head-track', 'Head Tracking', 'Track objects or people with head movement', 'perception', '["head_yaw","head_pitch"]', '1.0.0')
ON CONFLICT (id) DO NOTHING;
