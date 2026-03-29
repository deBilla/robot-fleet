/**
 * Procedural humanoid robot mesh driven by MuJoCo joint telemetry.
 *
 * Builds a skeleton of capsules/spheres matching the MuJoCo Humanoid-v4
 * body tree. Joint angles from WebSocket telemetry animate the mesh at 60 FPS
 * via interpolation from the 10 Hz update rate.
 *
 * Coordinate mapping: MuJoCo Z-up -> Three.js Y-up
 *   (mx, my, mz) -> (mx, mz, -my)
 */

import { useRef, useMemo } from 'react';
import { useFrame } from '@react-three/fiber';
import * as THREE from 'three';
import type { TelemetryEvent } from '../hooks/useWebSocket';

const BODY_COLOR = '#4a5568';
const JOINT_COLOR = '#60a5fa';
const HEAD_COLOR = '#718096';

// Convert MuJoCo coords to Three.js Y-up
function m(mx: number, my: number, mz: number): THREE.Vector3 {
  return new THREE.Vector3(mx, mz, -my);
}

// Create a capsule mesh between two MuJoCo points
function Capsule({ from, to, radius, color }: {
  from: [number, number, number]; to: [number, number, number]; radius: number; color?: string;
}) {
  const p1 = m(...from);
  const p2 = m(...to);
  const mid = p1.clone().add(p2).multiplyScalar(0.5);
  const dir = p2.clone().sub(p1);
  const len = dir.length();

  const quat = useMemo(() => {
    const q = new THREE.Quaternion();
    q.setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir.clone().normalize());
    return q;
  }, [dir.x, dir.y, dir.z]);

  return (
    <mesh position={mid} quaternion={quat} castShadow>
      <capsuleGeometry args={[radius, len, 4, 8]} />
      <meshStandardMaterial color={color || BODY_COLOR} metalness={0.3} roughness={0.7} />
    </mesh>
  );
}

interface Props {
  state: TelemetryEvent;
  color?: string;
}

export function HumanoidRobot({ state, color }: Props) {
  const groupRef = useRef<THREE.Group>(null!);
  const prevState = useRef<Record<string, number>>({});
  const currentState = useRef<Record<string, number>>({});
  const lastUpdate = useRef(0);

  // Joint refs for animation
  const lwRef = useRef<THREE.Group>(null!);    // lwaist: abdomen_z, abdomen_y
  const pelRef = useRef<THREE.Group>(null!);   // pelvis: abdomen_x
  const rthRef = useRef<THREE.Group>(null!);   // right thigh: hip_x, hip_z, hip_y
  const rshRef = useRef<THREE.Group>(null!);   // right shin: knee
  const lthRef = useRef<THREE.Group>(null!);   // left thigh
  const lshRef = useRef<THREE.Group>(null!);   // left shin
  const ruaRef = useRef<THREE.Group>(null!);   // right upper arm: shoulder1, shoulder2
  const rlaRef = useRef<THREE.Group>(null!);   // right lower arm: elbow
  const luaRef = useRef<THREE.Group>(null!);   // left upper arm
  const llaRef = useRef<THREE.Group>(null!);   // left lower arm

  const bodyColor = color || BODY_COLOR;

  useFrame(() => {
    if (!groupRef.current) return;

    const joints = state.joints || {};

    // Update interpolation state when new data arrives
    const now = performance.now();
    if (Object.keys(joints).length > 0 && now - lastUpdate.current > 50) {
      prevState.current = { ...currentState.current };
      currentState.current = { ...joints };
      lastUpdate.current = now;
    }

    // Interpolation factor
    const elapsed = now - lastUpdate.current;
    const t = Math.min(elapsed / 100, 1); // 100ms = 10Hz

    const lerp = (name: string) => {
      const prev = prevState.current[name] ?? 0;
      const curr = currentState.current[name] ?? 0;
      return prev + (curr - prev) * t;
    };

    // Root position (MuJoCo Z-up -> Three.js Y-up)
    groupRef.current.position.set(state.pos_x, state.pos_z || 0, -(state.pos_y));

    // Apply joint rotations
    // lwaist: abdomen_z (yaw around Z -> Y in three), abdomen_y (pitch around Y -> -Z)
    if (lwRef.current) {
      const az = lerp('head_pan');
      const ay = lerp('head_tilt');
      lwRef.current.rotation.set(0, az, 0);
      lwRef.current.rotateZ(-ay); // pitch
    }

    // pelvis: abdomen_x (roll around X -> X in three)
    if (pelRef.current) {
      pelRef.current.rotation.set(lerp('head_pan') * 0.1, 0, 0); // minimal effect
    }

    // Right thigh: hip_x (roll), hip_z (yaw), hip_y (pitch)
    if (rthRef.current) {
      const q = new THREE.Quaternion();
      const hx = lerp('right_hip_roll');
      const hz = lerp('right_hip_yaw');
      const hy = lerp('right_hip_pitch');
      q.setFromEuler(new THREE.Euler(hx, hz, -hy, 'XYZ'));
      rthRef.current.quaternion.copy(q);
    }

    // Right knee
    if (rshRef.current) {
      rshRef.current.rotation.set(0, 0, lerp('right_knee'));
    }

    // Left thigh
    if (lthRef.current) {
      const q = new THREE.Quaternion();
      const hx = lerp('left_hip_roll');
      const hz = lerp('left_hip_yaw');
      const hy = lerp('left_hip_pitch');
      q.setFromEuler(new THREE.Euler(-hx, -hz, -hy, 'XYZ'));
      lthRef.current.quaternion.copy(q);
    }

    // Left knee
    if (lshRef.current) {
      lshRef.current.rotation.set(0, 0, lerp('left_knee'));
    }

    // Right upper arm: shoulder1, shoulder2
    if (ruaRef.current) {
      const s1 = lerp('right_shoulder_pitch');
      const s2 = lerp('right_shoulder_roll');
      ruaRef.current.rotation.set(s1, s2, 0);
    }

    // Right lower arm: elbow
    if (rlaRef.current) {
      rlaRef.current.rotation.set(0, 0, lerp('right_elbow'));
    }

    // Left upper arm
    if (luaRef.current) {
      const s1 = lerp('left_shoulder_pitch');
      const s2 = lerp('left_shoulder_roll');
      luaRef.current.rotation.set(s1, -s2, 0);
    }

    // Left lower arm: elbow
    if (llaRef.current) {
      llaRef.current.rotation.set(0, 0, lerp('left_elbow'));
    }
  });

  return (
    <group ref={groupRef}>
      {/* Torso */}
      <Capsule from={[0, -0.07, 0]} to={[0, 0.07, 0]} radius={0.07} color={bodyColor} />

      {/* Head */}
      <mesh position={m(0, 0, 0.19)} castShadow>
        <sphereGeometry args={[0.09, 16, 16]} />
        <meshStandardMaterial color={HEAD_COLOR} metalness={0.4} roughness={0.6} />
      </mesh>
      {/* Eyes */}
      <mesh position={m(0.06, -0.03, 0.2)}>
        <sphereGeometry args={[0.02, 8, 8]} />
        <meshStandardMaterial color={JOINT_COLOR} emissive={JOINT_COLOR} emissiveIntensity={0.5} />
      </mesh>
      <mesh position={m(0.06, 0.03, 0.2)}>
        <sphereGeometry args={[0.02, 8, 8]} />
        <meshStandardMaterial color={JOINT_COLOR} emissive={JOINT_COLOR} emissiveIntensity={0.5} />
      </mesh>

      {/* Upper waist capsule */}
      <Capsule from={[-0.01, -0.06, -0.12]} to={[-0.01, 0.06, -0.12]} radius={0.06} color={bodyColor} />

      {/* Lower waist group (abdomen joints) */}
      <group ref={lwRef} position={m(-0.01, 0, -0.26)}>
        <Capsule from={[0, -0.06, 0]} to={[0, 0.06, 0]} radius={0.06} color={bodyColor} />

        {/* Pelvis group */}
        <group ref={pelRef} position={m(0, 0, -0.165)}>
          {/* Butt */}
          <Capsule from={[-0.02, -0.07, 0]} to={[-0.02, 0.07, 0]} radius={0.09} color={bodyColor} />

          {/* Right Thigh */}
          <group ref={rthRef} position={m(0, -0.1, -0.04)}>
            <Capsule from={[0, 0, 0]} to={[0, 0.01, -0.34]} radius={0.06} color={bodyColor} />
            {/* Joint indicator */}
            <mesh position={m(0, 0, 0)}>
              <sphereGeometry args={[0.035, 8, 8]} />
              <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
            </mesh>

            {/* Right Shin */}
            <group ref={rshRef} position={m(0, 0.01, -0.403)}>
              <Capsule from={[0, 0, 0]} to={[0, 0, -0.3]} radius={0.049} color={bodyColor} />
              <mesh position={m(0, 0, 0)}>
                <sphereGeometry args={[0.03, 8, 8]} />
                <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
              </mesh>
              {/* Right Foot */}
              <mesh position={m(0, 0, -0.35)} castShadow>
                <sphereGeometry args={[0.075, 12, 12]} />
                <meshStandardMaterial color={bodyColor} />
              </mesh>
            </group>
          </group>

          {/* Left Thigh */}
          <group ref={lthRef} position={m(0, 0.1, -0.04)}>
            <Capsule from={[0, 0, 0]} to={[0, -0.01, -0.34]} radius={0.06} color={bodyColor} />
            <mesh position={m(0, 0, 0)}>
              <sphereGeometry args={[0.035, 8, 8]} />
              <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
            </mesh>

            {/* Left Shin */}
            <group ref={lshRef} position={m(0, -0.01, -0.403)}>
              <Capsule from={[0, 0, 0]} to={[0, 0, -0.3]} radius={0.049} color={bodyColor} />
              <mesh position={m(0, 0, 0)}>
                <sphereGeometry args={[0.03, 8, 8]} />
                <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
              </mesh>
              {/* Left Foot */}
              <mesh position={m(0, 0, -0.35)} castShadow>
                <sphereGeometry args={[0.075, 12, 12]} />
                <meshStandardMaterial color={bodyColor} />
              </mesh>
            </group>
          </group>
        </group>
      </group>

      {/* Right Upper Arm */}
      <group ref={ruaRef} position={m(0, -0.17, 0.06)}>
        <Capsule from={[0, 0, 0]} to={[0.16, -0.16, -0.16]} radius={0.04} color={bodyColor} />
        <mesh position={m(0, 0, 0)}>
          <sphereGeometry args={[0.03, 8, 8]} />
          <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
        </mesh>

        {/* Right Lower Arm */}
        <group ref={rlaRef} position={m(0.18, -0.18, -0.18)}>
          <Capsule from={[0.01, 0.01, 0.01]} to={[0.17, 0.17, 0.17]} radius={0.031} color={bodyColor} />
          {/* Right Hand */}
          <mesh position={m(0.18, 0.18, 0.18)}>
            <sphereGeometry args={[0.04, 10, 10]} />
            <meshStandardMaterial color={bodyColor} />
          </mesh>
        </group>
      </group>

      {/* Left Upper Arm */}
      <group ref={luaRef} position={m(0, 0.17, 0.06)}>
        <Capsule from={[0, 0, 0]} to={[0.16, 0.16, -0.16]} radius={0.04} color={bodyColor} />
        <mesh position={m(0, 0, 0)}>
          <sphereGeometry args={[0.03, 8, 8]} />
          <meshStandardMaterial color={JOINT_COLOR} metalness={0.5} />
        </mesh>

        {/* Left Lower Arm */}
        <group ref={llaRef} position={m(0.18, 0.18, -0.18)}>
          <Capsule from={[0.01, -0.01, 0.01]} to={[0.17, -0.17, 0.17]} radius={0.031} color={bodyColor} />
          {/* Left Hand */}
          <mesh position={m(0.18, -0.18, 0.18)}>
            <sphereGeometry args={[0.04, 10, 10]} />
            <meshStandardMaterial color={bodyColor} />
          </mesh>
        </group>
      </group>

      {/* Status LED on chest */}
      <mesh position={m(0.05, 0, -0.05)}>
        <sphereGeometry args={[0.015, 8, 8]} />
        <meshStandardMaterial
          color={state.status === 'active' ? '#22c55e' : state.status === 'charging' ? '#f59e0b' : '#ef4444'}
          emissive={state.status === 'active' ? '#22c55e' : state.status === 'charging' ? '#f59e0b' : '#ef4444'}
          emissiveIntensity={0.8}
        />
      </mesh>

      {/* Robot ID label - floating text would need drei Text, use a simple indicator instead */}
      <mesh position={[0, 2.0, 0]}>
        <sphereGeometry args={[0.03, 6, 6]} />
        <meshStandardMaterial
          color={color || JOINT_COLOR}
          emissive={color || JOINT_COLOR}
          emissiveIntensity={0.6}
        />
      </mesh>
    </group>
  );
}
