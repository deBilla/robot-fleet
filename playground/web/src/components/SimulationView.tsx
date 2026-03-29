/**
 * Three.js 3D simulation view — renders the MuJoCo lab room with
 * animated humanoid robots driven by WebSocket telemetry.
 */

import { Canvas } from '@react-three/fiber';
import { OrbitControls } from '@react-three/drei';
import { LabRoom } from './LabRoom';
import { HumanoidRobot } from './HumanoidRobot';
import type { TelemetryEvent } from '../hooks/useWebSocket';

const ROBOT_COLORS = [
  '#60a5fa', // blue
  '#f59e0b', // amber
  '#22c55e', // green
  '#ef4444', // red
  '#a78bfa', // purple
  '#ec4899', // pink
  '#06b6d4', // cyan
  '#f97316', // orange
];

interface Props {
  robotStates: Map<string, TelemetryEvent>;
  connected: boolean;
}

export function SimulationView({ robotStates, connected }: Props) {
  const robots = Array.from(robotStates.entries());

  return (
    <div style={{ height: 420, background: '#0a0a0a', borderRadius: 8, overflow: 'hidden', position: 'relative' }}>
      <Canvas
        camera={{ position: [0, 6, 8], fov: 60, near: 0.1, far: 100 }}
        shadows
        gl={{ antialias: true }}
      >
        {/* Lighting matching lab_room.xml */}
        <ambientLight intensity={0.3} color="#ccccdd" />
        <directionalLight
          position={[0, 8, 0]}
          intensity={0.9}
          color="#e8e8ff"
          castShadow
          shadow-mapSize={[1024, 1024]}
        />
        <directionalLight position={[5, 5, -3]} intensity={0.4} color="#aaaacc" />
        <directionalLight position={[-5, 5, 3]} intensity={0.3} color="#aaaacc" />

        {/* Fog for depth */}
        <fog attach="fog" args={['#0a0a0a', 12, 25]} />

        {/* Lab room static geometry */}
        <LabRoom />

        {/* Animated humanoid robots */}
        {robots.map(([id, state], index) => (
          <HumanoidRobot
            key={id}
            state={state}
            color={ROBOT_COLORS[index % ROBOT_COLORS.length]}
          />
        ))}

        {/* Camera controls */}
        <OrbitControls
          target={[0, 1.0, 0]}
          maxPolarAngle={Math.PI * 0.48}
          minDistance={2}
          maxDistance={18}
          enableDamping
          dampingFactor={0.05}
        />
      </Canvas>

      {/* Overlay: connection status */}
      {!connected && (
        <div style={{
          position: 'absolute', bottom: 10, left: 10,
          padding: '4px 10px', borderRadius: 4,
          background: 'rgba(0,0,0,0.7)', color: '#f59e0b',
          fontSize: 11, fontFamily: 'var(--font-mono)',
        }}>
          Waiting for telemetry...
        </div>
      )}

      {/* Overlay: robot count */}
      <div style={{
        position: 'absolute', bottom: 10, right: 10,
        padding: '4px 10px', borderRadius: 4,
        background: 'rgba(0,0,0,0.7)', color: '#94a3b8',
        fontSize: 10, fontFamily: 'var(--font-mono)',
      }}>
        {robots.length} robot{robots.length !== 1 ? 's' : ''} | Three.js + MuJoCo
      </div>
    </div>
  );
}
