import { useState, useCallback, useRef } from 'react';
import { api } from '../api';
import { usePolling } from '../hooks/usePolling';
import { useWebSocket } from '../hooks/useWebSocket';
import type { TelemetryEvent } from '../hooks/useWebSocket';

interface InferenceResult {
  predicted_actions: { joint: string; position: number; velocity: number; torque: number }[];
  confidence: number;
  model_id: string;
  latency_ms: number;
  action_horizon: number;
  diffusion_steps: number;
}

interface FleetMetrics {
  total_robots: number;
  active_robots: number;
  idle_robots: number;
  error_robots: number;
  avg_battery: number;
}

export function Dashboard() {
  const [selectedRobot, setSelectedRobot] = useState<string | null>(null);
  const [inferenceResult, setInferenceResult] = useState<InferenceResult | null>(null);
  const { connected, events, robotStates } = useWebSocket();

  const metricsFetcher = useCallback(async () => {
    const res = await api.getFleetMetrics();
    return res.data as FleetMetrics;
  }, []);
  const { data: metrics } = usePolling(metricsFetcher, 2000);

  const robots = Array.from(robotStates.values());
  const selected = selectedRobot ? robotStates.get(selectedRobot) : null;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Stats Row */}
      <div className="grid-4">
        <div className="stat">
          <div className="stat-label">Total Robots</div>
          <div className="stat-value accent">{metrics?.total_robots ?? robots.length}</div>
        </div>
        <div className="stat">
          <div className="stat-label">Active</div>
          <div className="stat-value success">{metrics?.active_robots ?? robots.filter(r => r.status === 'active').length}</div>
        </div>
        <div className="stat">
          <div className="stat-label">Charging</div>
          <div className="stat-value warning">{metrics?.idle_robots ?? robots.filter(r => r.status === 'charging').length}</div>
        </div>
        <div className="stat">
          <div className="stat-label">Errors</div>
          <div className="stat-value danger">{metrics?.error_robots ?? 0}</div>
        </div>
      </div>

      {/* Swarm Controls */}
      <SwarmControls robotIds={robots.map(r => r.robot_id)} />

      {/* Map + Detail + Inference */}
      <div className="grid-2">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="card">
            <div className="card-header">
              <div>
                <div className="card-title">Fleet Map</div>
                <div className="card-subtitle">
                  {connected ? `Live - ${robots.length} robots` : 'Connecting...'}
                </div>
              </div>
              <div className="connection-status">
                <div className={`status-dot ${connected ? 'connected' : ''}`} />
                {connected ? 'Live' : 'Offline'}
              </div>
            </div>
            <FleetMap robots={robots} selected={selectedRobot} onSelect={setSelectedRobot} inferenceResult={inferenceResult} />
          </div>

          {/* Inference Panel — always visible below map */}
          <InferencePanel
            selectedRobot={selected}
            onResult={setInferenceResult}
            result={inferenceResult}
          />
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {selected ? (
            <>
              <RobotDetail robot={selected} />
              <JointVisualizer data={selected} inferenceActions={inferenceResult?.predicted_actions} />
              <SensorPanel data={selected} />
            </>
          ) : (
            <div className="card" style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
              <div style={{ textAlign: 'center', color: 'var(--text-muted)' }}>
                <div style={{ fontSize: 32, marginBottom: 8 }}>&#8592;</div>
                <div>Click a robot on the map to see sensor data</div>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Telemetry Stream */}
      <div className="card">
        <div className="card-header">
          <div className="card-title">Telemetry Stream</div>
          <div className="card-subtitle">{events.length} events received</div>
        </div>
        <TelemetryStream events={events.slice(-50)} />
      </div>
    </div>
  );
}

// --- Swarm Controls ---

const SWARM_ACTIONS = [
  { type: 'dance', label: 'Dance', icon: '💃' },
  { type: 'wave', label: 'Wave', icon: '👋' },
  { type: 'jump', label: 'Jump', icon: '🦘' },
  { type: 'bow', label: 'Bow', icon: '🙇' },
  { type: 'sit', label: 'Sit', icon: '🪑' },
  { type: 'look_around', label: 'Look', icon: '👀' },
  { type: 'stretch', label: 'Stretch', icon: '🧘' },
  { type: 'stop', label: 'Stop All', icon: '🛑' },
];

// Pixel font: each letter is a 5-tall grid of points, coordinates as [col, row]
const LETTER_POINTS: Record<string, [number, number][]> = {
  A: [[0,4],[0,3],[0,2],[0,1],[1,0],[2,0],[3,1],[3,2],[3,3],[3,4],[1,2],[2,2]],
  B: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,0],[3,1],[2,2],[1,2],[3,3],[2,4],[1,4]],
  C: [[1,0],[2,0],[3,0],[0,1],[0,2],[0,3],[1,4],[2,4],[3,4]],
  D: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,1],[2,2],[2,3],[1,4]],
  E: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,0],[1,2],[2,2],[1,4],[2,4]],
  F: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,0],[1,2],[2,2]],
  G: [[1,0],[2,0],[3,0],[0,1],[0,2],[0,3],[1,4],[2,4],[3,4],[3,3],[2,2]],
  H: [[0,0],[0,1],[0,2],[0,3],[0,4],[3,0],[3,1],[3,2],[3,3],[3,4],[1,2],[2,2]],
  I: [[0,0],[1,0],[2,0],[1,1],[1,2],[1,3],[0,4],[1,4],[2,4]],
  J: [[2,0],[2,1],[2,2],[2,3],[1,4],[0,3]],
  K: [[0,0],[0,1],[0,2],[0,3],[0,4],[2,0],[1,1],[1,2],[2,3],[3,4]],
  L: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,4],[2,4],[3,4]],
  M: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,1],[2,2],[3,1],[4,0],[4,1],[4,2],[4,3],[4,4]],
  N: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,1],[2,2],[3,3],[3,0],[3,1],[3,2],[3,4]],
  O: [[1,0],[2,0],[0,1],[0,2],[0,3],[3,1],[3,2],[3,3],[1,4],[2,4]],
  P: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,0],[3,1],[2,2],[1,2]],
  R: [[0,0],[0,1],[0,2],[0,3],[0,4],[1,0],[2,0],[3,1],[2,2],[1,2],[2,3],[3,4]],
  S: [[1,0],[2,0],[3,0],[0,1],[1,2],[2,2],[3,3],[0,4],[1,4],[2,4]],
  T: [[0,0],[1,0],[2,0],[3,0],[4,0],[2,1],[2,2],[2,3],[2,4]],
  U: [[0,0],[0,1],[0,2],[0,3],[3,0],[3,1],[3,2],[3,3],[1,4],[2,4]],
  V: [[0,0],[0,1],[0,2],[1,3],[2,4],[3,3],[4,2],[4,1],[4,0]],
  W: [[0,0],[0,1],[0,2],[0,3],[1,4],[2,3],[3,4],[4,3],[4,2],[4,1],[4,0]],
  X: [[0,0],[0,4],[1,1],[1,3],[2,2],[3,1],[3,3],[4,0],[4,4]],
  Y: [[0,0],[1,1],[2,2],[2,3],[2,4],[3,1],[4,0]],
  Z: [[0,0],[1,0],[2,0],[3,0],[3,1],[2,2],[1,3],[0,4],[1,4],[2,4],[3,4]],
};

const FORMATIONS: Record<string, (n: number) => [number, number][]> = {
  circle: (n) => Array.from({ length: n }, (_, i) => {
    const a = (i / n) * Math.PI * 2;
    const r = Math.min(25, 1.2 * Math.sqrt(n));
    return [Math.cos(a) * r, Math.sin(a) * r] as [number, number];
  }),
  line: (n) => Array.from({ length: n }, (_, i) => [
    (i - n / 2) * 0.35, 0,
  ] as [number, number]),
  vshape: (n) => Array.from({ length: n }, (_, i) => {
    const half = Math.floor(n / 2);
    const side = i < half ? -1 : 1;
    const idx = i < half ? i : i - half;
    return [side * (idx + 1) * 0.5, idx * 0.5] as [number, number];
  }),
  grid: (n) => {
    const cols = Math.ceil(Math.sqrt(n));
    const sp = Math.min(3, 50 / cols);
    return Array.from({ length: n }, (_, i) => [
      (i % cols - cols / 2) * sp, (Math.floor(i / cols) - cols / 2) * sp,
    ] as [number, number]);
  },
  scatter: (n) => Array.from({ length: n }, () => [
    (Math.random() - 0.5) * 20, (Math.random() - 0.5) * 20,
  ] as [number, number]),
};

function getLetterPositions(letter: string, maxRobots: number): [number, number][] {
  const ch = letter.toUpperCase();
  const pts = LETTER_POINTS[ch];
  if (!pts) return FORMATIONS.circle(maxRobots);

  // Scale points to world coords, centered at origin
  const scale = Math.max(3, Math.sqrt(maxRobots) * 0.6);
  const allPts = pts.map(([c, r]) => [
    (c - 2) * scale,
    (r - 2) * scale,
  ] as [number, number]);

  // Distribute robots along letter points with spread
  if (maxRobots >= allPts.length) {
    const result = [...allPts];
    const spread = scale * 0.3;
    for (let i = allPts.length; i < maxRobots; i++) {
      const base = allPts[i % allPts.length];
      result.push([base[0] + (Math.random() - 0.5) * spread, base[1] + (Math.random() - 0.5) * spread]);
    }
    return result;
  }
  // Fewer robots: evenly sample from points
  return Array.from({ length: maxRobots }, (_, i) =>
    allPts[Math.floor(i * allPts.length / maxRobots)]
  );
}

function SwarmControls({ robotIds }: { robotIds: string[] }) {
  const [active, setActive] = useState<string | null>(null);
  const [formationInput, setFormationInput] = useState('');

  const sendToAll = async (actionType: string) => {
    setActive(actionType);
    const params = actionType === 'stop'
      ? { emergency: false }
      : { instruction: actionType };
    await Promise.all(
      robotIds.map(id => api.sendCommand(id, actionType, params))
    );
    setTimeout(() => setActive(null), 2000);
  };

  const sendFormation = async (positions: [number, number][]) => {
    setActive('formation');
    await Promise.all(
      robotIds.map((id, i) => {
        const [x, y] = positions[i % positions.length];
        return api.sendCommand(id, 'move', { x, y });
      })
    );
    setTimeout(() => setActive(null), 2000);
  };

  const sendLetterFormation = async () => {
    const char = formationInput.trim();
    if (!char) return;
    const positions = getLetterPositions(char[0], robotIds.length);
    await sendFormation(positions);
  };

  if (robotIds.length === 0) return null;

  return (
    <div className="card">
      <div className="card-header">
        <div>
          <div className="card-title">Swarm Control</div>
          <div className="card-subtitle">Command all {robotIds.length} robots at once</div>
        </div>
      </div>

      {/* Actions */}
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
        {SWARM_ACTIONS.map(({ type, label, icon }) => (
          <button
            key={type}
            className={`btn btn-sm ${type === 'stop' ? 'btn-danger' : 'btn-primary'}`}
            onClick={() => sendToAll(type)}
            disabled={active !== null}
            style={{
              display: 'flex', alignItems: 'center', gap: 5, padding: '6px 14px',
              ...(active === type ? { background: 'var(--success)' } : {}),
            }}
          >
            <span>{icon}</span>
            <span>{active === type ? 'Sent!' : label}</span>
          </button>
        ))}
      </div>

      {/* Formations */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'end', flexWrap: 'wrap' }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginRight: 4, alignSelf: 'center' }}>
          Formations:
        </div>
        {Object.keys(FORMATIONS).map((name) => (
          <button
            key={name}
            className="btn btn-sm btn-primary"
            onClick={() => sendFormation(FORMATIONS[name](robotIds.length))}
            disabled={active !== null}
            style={{
              padding: '4px 12px', textTransform: 'capitalize',
              ...(active === 'formation' ? { background: 'var(--success)' } : {}),
            }}
          >
            {name}
          </button>
        ))}

        <div style={{ display: 'flex', gap: 6, alignItems: 'center', marginLeft: 8 }}>
          <input
            value={formationInput}
            onChange={(e) => setFormationInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && sendLetterFormation()}
            placeholder="Type a letter..."
            style={{ width: 120, padding: '4px 10px', fontSize: 13 }}
            maxLength={1}
          />
          <button
            className="btn btn-sm btn-primary"
            onClick={sendLetterFormation}
            disabled={active !== null || !formationInput.trim()}
            style={active === 'formation' ? { background: 'var(--success)' } : {}}
          >
            Form Letter
          </button>
        </div>
      </div>
    </div>
  );
}

// --- Fleet Map with Humanoid Figures ---

function FleetMap({ robots, selected, onSelect, inferenceResult }: {
  robots: TelemetryEvent[]; selected: string | null; onSelect: (id: string) => void;
  inferenceResult?: InferenceResult | null;
}) {
  const mapToPixel = (x: number, y: number) => {
    const px = ((x + 35) / 70) * 100;
    const py = ((y + 35) / 70) * 100;
    return { left: `${Math.max(2, Math.min(98, px))}%`, top: `${Math.max(2, Math.min(98, py))}%` };
  };

  return (
    <div className="fleet-map">
      <div className="fleet-map-grid" />
      {robots.map((r) => {
        const pos = mapToPixel(r.pos_x, r.pos_y);
        const isSelected = selected === r.robot_id;
        return (
          <div
            key={r.robot_id}
            style={{
              position: 'absolute', left: pos.left, top: pos.top,
              transform: 'translate(-50%, -50%)',
              cursor: 'pointer', zIndex: isSelected ? 10 : 1,
            }}
            onClick={() => onSelect(r.robot_id)}
            title={`${r.robot_id} (${r.status})`}
          >
            <HumanoidFigure data={r} selected={isSelected} />
            {isSelected && inferenceResult && (
              <div style={{
                position: 'absolute', top: -8, right: -8,
                width: 14, height: 14, borderRadius: '50%',
                background: 'var(--accent)', border: '2px solid var(--bg-secondary)',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: 8, color: '#fff', fontWeight: 700,
              }}>
                AI
              </div>
            )}
            <div style={{
              textAlign: 'center', fontSize: 8, marginTop: 1,
              fontFamily: 'var(--font-mono)', color: isSelected ? 'var(--accent)' : 'var(--text-muted)',
              whiteSpace: 'nowrap',
            }}>
              {r.robot_id.replace('robot-', '#')}
            </div>
          </div>
        );
      })}
      {robots.length === 0 && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--text-muted)', fontSize: 14 }}>
          Waiting for robot telemetry...
        </div>
      )}
    </div>
  );
}

// Robotic humanoid figure with mechanical limbs, animated by real joint data
function HumanoidFigure({ data, selected }: { data: TelemetryEvent; selected: boolean }) {
  const d = data as any;
  const joints: Record<string, number> = d.joints || {};
  const s = selected ? 44 : 26; // overall size (smaller for large fleets)
  const cx = s / 2;

  // Joint angles (radians)
  const lSP = joints['left_shoulder_pitch'] || 0;
  const rSP = joints['right_shoulder_pitch'] || 0;
  const lE = joints['left_elbow'] || 0;
  const rE = joints['right_elbow'] || 0;
  const lHP = joints['left_hip_pitch'] || 0;
  const rHP = joints['right_hip_pitch'] || 0;
  const lK = joints['left_knee'] || 0;
  const rK = joints['right_knee'] || 0;

  // Proportions
  const headY = s * 0.10;
  const headW = s * 0.16;
  const headH = s * 0.12;
  const neckY = headY + headH / 2 + 1;
  const shoulderY = s * 0.26;
  const chestW = s * 0.22;
  const chestH = s * 0.15;
  const waistY = shoulderY + chestH;
  const hipY = s * 0.50;
  const pelvisW = s * 0.18;
  const pelvisH = hipY - waistY;
  const limbW = s * 0.065;
  const upperLen = s * 0.17;
  const lowerLen = s * 0.17;
  const jointR = s * 0.04;
  const handR = s * 0.03;
  const armOff = chestW / 2 + limbW / 2;
  const legOff = pelvisW / 2 - limbW / 2;

  // Forward kinematics
  const fk = (ox: number, oy: number, a1: number, l1: number, a2: number, l2: number) => {
    const mx = ox + Math.sin(a1) * l1;
    const my = oy + Math.cos(a1) * l1;
    const ex = mx + Math.sin(a1 + a2) * l2;
    const ey = my + Math.cos(a1 + a2) * l2;
    return { mx, my, ex, ey, a1, a2: a1 + a2 };
  };

  const lArm = fk(cx - armOff, shoulderY, lSP, upperLen, lE, lowerLen);
  const rArm = fk(cx + armOff, shoulderY, rSP, upperLen, rE, lowerLen);
  const lLeg = fk(cx - legOff, hipY, lHP, upperLen, lK, lowerLen);
  const rLeg = fk(cx + legOff, hipY, rHP, upperLen, rK, lowerLen);

  // Status color
  const c = data.status === 'active' ? '#22c55e'
    : data.status === 'charging' ? '#f59e0b'
    : data.status === 'error' ? '#ef4444' : '#64748b';
  const bodyFill = selected ? '#1e3a5f' : '#1a2233';
  const bodyStroke = selected ? '#3b82f6' : c;
  const accentC = selected ? '#60a5fa' : c;
  const eyeC = selected ? '#93c5fd' : c;
  const sw = selected ? 1.2 : 0.8;

  // Helper: rotated rounded rectangle (limb segment)
  const limbSeg = (ox: number, oy: number, ex: number, ey: number, w: number, key: string) => {
    const angle = Math.atan2(ex - ox, ey - oy);
    const len = Math.sqrt((ex - ox) ** 2 + (ey - oy) ** 2);
    const mcx = (ox + ex) / 2;
    const mcy = (oy + ey) / 2;
    const deg = -angle * 180 / Math.PI;
    return (
      <rect key={key} x={mcx - w / 2} y={mcy - len / 2} width={w} height={len} rx={w / 2.5}
        fill={bodyFill} stroke={bodyStroke} strokeWidth={sw}
        transform={`rotate(${deg} ${mcx} ${mcy})`} />
    );
  };

  // Joint ring
  const jointRing = (x: number, y: number, key: string) => (
    <g key={key}>
      <circle cx={x} cy={y} r={jointR} fill={bodyFill} stroke={accentC} strokeWidth={sw * 1.2} />
      <circle cx={x} cy={y} r={jointR * 0.4} fill={accentC} />
    </g>
  );

  return (
    <svg width={s} height={s * 1.05} viewBox={`0 0 ${s} ${s * 1.05}`} style={{ overflow: 'visible' }}>
      {/* Selection glow */}
      {selected && (
        <ellipse cx={cx} cy={s * 0.4} rx={s * 0.38} ry={s * 0.48} fill="none"
          stroke="var(--accent)" strokeWidth={1.5} opacity={0.2} strokeDasharray="3 3" />
      )}

      {/* --- Back limbs (right side, drawn first for overlap) --- */}
      {limbSeg(cx + legOff, hipY, rLeg.mx, rLeg.my, limbW, 'rl-upper')}
      {limbSeg(rLeg.mx, rLeg.my, rLeg.ex, rLeg.ey, limbW * 0.9, 'rl-lower')}
      {limbSeg(cx + armOff, shoulderY, rArm.mx, rArm.my, limbW * 0.85, 'ra-upper')}
      {limbSeg(rArm.mx, rArm.my, rArm.ex, rArm.ey, limbW * 0.75, 'ra-lower')}

      {/* --- Torso (chest plate) --- */}
      <rect x={cx - chestW / 2} y={shoulderY - 1} width={chestW} height={chestH} rx={s * 0.03}
        fill={bodyFill} stroke={bodyStroke} strokeWidth={sw * 1.2} />
      {/* Chest panel line */}
      <line x1={cx - chestW * 0.25} y1={shoulderY + chestH * 0.3} x2={cx + chestW * 0.25} y2={shoulderY + chestH * 0.3}
        stroke={accentC} strokeWidth={sw * 0.5} opacity={0.4} />
      <line x1={cx} y1={shoulderY + 2} x2={cx} y2={shoulderY + chestH - 2}
        stroke={accentC} strokeWidth={sw * 0.4} opacity={0.3} />

      {/* Pelvis / waist */}
      <rect x={cx - pelvisW / 2} y={waistY} width={pelvisW} height={pelvisH} rx={s * 0.02}
        fill={bodyFill} stroke={bodyStroke} strokeWidth={sw} />

      {/* Neck */}
      <rect x={cx - s * 0.025} y={neckY} width={s * 0.05} height={shoulderY - neckY}
        fill={bodyFill} stroke={bodyStroke} strokeWidth={sw * 0.6} rx={1} />

      {/* --- Head --- */}
      <rect x={cx - headW / 2} y={headY - headH / 2} width={headW} height={headH} rx={s * 0.035}
        fill={bodyFill} stroke={bodyStroke} strokeWidth={sw * 1.2} />
      {/* Visor / eyes */}
      <ellipse cx={cx - headW * 0.18} cy={headY} rx={s * 0.02} ry={s * 0.015} fill={eyeC} opacity={0.9} />
      <ellipse cx={cx + headW * 0.18} cy={headY} rx={s * 0.02} ry={s * 0.015} fill={eyeC} opacity={0.9} />
      {/* Antenna */}
      <line x1={cx} y1={headY - headH / 2} x2={cx} y2={headY - headH / 2 - s * 0.04}
        stroke={accentC} strokeWidth={sw} strokeLinecap="round" />
      <circle cx={cx} cy={headY - headH / 2 - s * 0.04} r={s * 0.012} fill={accentC} />

      {/* --- Front limbs (left side) --- */}
      {limbSeg(cx - legOff, hipY, lLeg.mx, lLeg.my, limbW, 'll-upper')}
      {limbSeg(lLeg.mx, lLeg.my, lLeg.ex, lLeg.ey, limbW * 0.9, 'll-lower')}
      {limbSeg(cx - armOff, shoulderY, lArm.mx, lArm.my, limbW * 0.85, 'la-upper')}
      {limbSeg(lArm.mx, lArm.my, lArm.ex, lArm.ey, limbW * 0.75, 'la-lower')}

      {/* --- Joints (mechanical rings) --- */}
      {jointRing(cx - armOff, shoulderY, 'j-ls')}
      {jointRing(cx + armOff, shoulderY, 'j-rs')}
      {jointRing(lArm.mx, lArm.my, 'j-le')}
      {jointRing(rArm.mx, rArm.my, 'j-re')}
      {jointRing(cx - legOff, hipY, 'j-lh')}
      {jointRing(cx + legOff, hipY, 'j-rh')}
      {jointRing(lLeg.mx, lLeg.my, 'j-lk')}
      {jointRing(rLeg.mx, rLeg.my, 'j-rk')}

      {/* --- Feet (armored boots with ankle joint + sole) --- */}
      {[
        { leg: lLeg, key: 'lf' },
        { leg: rLeg, key: 'rf' },
      ].map(({ leg, key }) => {
        // Ankle joint ring
        const ankleR = jointR * 0.9;
        // Boot: extends forward from ankle, rotated to match lower leg
        const bootW = s * 0.10;
        const bootH = s * 0.05;
        const soleH = s * 0.02;
        const lowerAngle = leg.a2;
        // Foot points forward (horizontal-ish) with slight tilt from leg angle
        const footAngle = lowerAngle * 0.15; // mostly flat, slight influence from leg
        const bootCx = leg.ex + Math.sin(footAngle) * bootW * 0.2;
        const bootCy = leg.ey + Math.cos(footAngle) * bootH * 0.3;
        const deg = -footAngle * 180 / Math.PI;
        return (
          <g key={key}>
            {/* Ankle joint */}
            <circle cx={leg.ex} cy={leg.ey} r={ankleR} fill={bodyFill} stroke={accentC} strokeWidth={sw} />
            <circle cx={leg.ex} cy={leg.ey} r={ankleR * 0.4} fill={accentC} />
            {/* Boot upper */}
            <rect
              x={bootCx - bootW / 2} y={bootCy}
              width={bootW} height={bootH} rx={s * 0.012}
              fill={bodyFill} stroke={bodyStroke} strokeWidth={sw}
              transform={`rotate(${deg} ${bootCx} ${bootCy + bootH / 2})`}
            />
            {/* Sole (slightly wider, darker) */}
            <rect
              x={bootCx - bootW * 0.55} y={bootCy + bootH - soleH * 0.5}
              width={bootW * 1.1} height={soleH} rx={s * 0.008}
              fill={bodyStroke} stroke={bodyStroke} strokeWidth={sw * 0.5}
              transform={`rotate(${deg} ${bootCx} ${bootCy + bootH / 2})`}
            />
            {/* Toe cap line */}
            <line
              x1={bootCx + bootW * 0.2} y1={bootCy + 1}
              x2={bootCx + bootW * 0.2} y2={bootCy + bootH - 1}
              stroke={accentC} strokeWidth={sw * 0.5} opacity={0.3}
              transform={`rotate(${deg} ${bootCx} ${bootCy + bootH / 2})`}
            />
          </g>
        );
      })}

      {/* --- Hands (mechanical) --- */}
      <circle cx={lArm.ex} cy={lArm.ey} r={handR} fill={bodyFill} stroke={accentC} strokeWidth={sw} />
      <circle cx={rArm.ex} cy={rArm.ey} r={handR} fill={bodyFill} stroke={accentC} strokeWidth={sw} />

      {/* Status LED on chest */}
      <circle cx={cx} cy={shoulderY + chestH * 0.7} r={s * 0.018} fill={c}>
        {data.status === 'active' && (
          <animate attributeName="opacity" values="1;0.4;1" dur="2s" repeatCount="indefinite" />
        )}
      </circle>
    </svg>
  );
}

// --- Robot Detail Panel ---

function RobotDetail({ robot }: { robot: TelemetryEvent }) {
  const d = robot as any;
  const batteryPct = Math.round(robot.battery_level * 100);
  const batteryColor = batteryPct > 50 ? 'var(--success)' : batteryPct > 20 ? 'var(--warning)' : 'var(--danger)';
  const voltage = (d.battery_voltage || 48 * robot.battery_level).toFixed(1);
  const [moveX, setMoveX] = useState('');
  const [moveY, setMoveY] = useState('');
  const [semanticInput, setSemanticInput] = useState('');

  const sendSemantic = async () => {
    if (!semanticInput.trim()) return;
    await api.semanticCommand(robot.robot_id, semanticInput);
    setSemanticInput('');
  };

  const sendMoveTo = async () => {
    const x = parseFloat(moveX) || robot.pos_x;
    const y = parseFloat(moveY) || robot.pos_y;
    await api.sendCommand(robot.robot_id, 'move', { x, y });
    setMoveX('');
    setMoveY('');
  };

  return (
    <div className="robot-detail" style={{ overflow: 'auto', maxHeight: 520 }}>
      <div className="robot-detail-header">
        <div className="robot-detail-id">{robot.robot_id}</div>
        <div className={`badge ${robot.status}`}>{robot.status}</div>
      </div>

      {/* Stats */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6, marginBottom: 10 }}>
        <div style={{ fontSize: 11 }}><span style={{ color: 'var(--text-muted)' }}>Pos:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>({robot.pos_x.toFixed(1)}, {robot.pos_y.toFixed(1)})</span></div>
        <div style={{ fontSize: 11 }}><span style={{ color: 'var(--text-muted)' }}>Bat:</span> <span style={{ fontFamily: 'var(--font-mono)', color: batteryColor }}>{batteryPct}% ({voltage}V)</span></div>
        <div style={{ fontSize: 11 }}><span style={{ color: 'var(--text-muted)' }}>DOF:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>{Object.keys(d.joints || {}).length}</span></div>
        <div style={{ fontSize: 11 }}><span style={{ color: 'var(--text-muted)' }}>Vel:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>{(d.odom_vel_x || 0).toFixed(2)} m/s</span></div>
      </div>
      <div className="battery-bar" style={{ marginBottom: 12 }}>
        <div className="battery-fill" style={{ width: `${batteryPct}%`, background: batteryColor }} />
      </div>

      {/* Semantic / Natural Language */}
      <div style={{ marginBottom: 10 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 4 }}>Talk to Robot</div>
        <div style={{ display: 'flex', gap: 6 }}>
          <input
            value={semanticInput}
            onChange={(e) => setSemanticInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && sendSemantic()}
            placeholder="e.g. dance, wave hello, sit down..."
            style={{ flex: 1, padding: '5px 8px', fontSize: 12 }}
          />
          <button className="btn btn-sm btn-primary" onClick={sendSemantic} disabled={!semanticInput.trim()}>
            Send
          </button>
        </div>
      </div>

      {/* Move To */}
      <div style={{ marginBottom: 10 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 4 }}>Move To Position</div>
        <div style={{ display: 'flex', gap: 6 }}>
          <input value={moveX} onChange={(e) => setMoveX(e.target.value)} placeholder="x" style={{ width: 55, padding: '5px 8px', fontSize: 12 }} />
          <input value={moveY} onChange={(e) => setMoveY(e.target.value)} placeholder="y" style={{ width: 55, padding: '5px 8px', fontSize: 12 }}
            onKeyDown={(e) => e.key === 'Enter' && sendMoveTo()} />
          <button className="btn btn-sm btn-primary" onClick={sendMoveTo}>Go</button>
        </div>
      </div>

      {/* Quick Actions */}
      <div style={{ marginBottom: 10 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 4 }}>Actions</div>
        <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          {[
            { type: 'dance', label: '💃 Dance' },
            { type: 'wave', label: '👋 Wave' },
            { type: 'jump', label: '🦘 Jump' },
            { type: 'bow', label: '🙇 Bow' },
            { type: 'sit', label: '🪑 Sit' },
            { type: 'look_around', label: '👀 Look' },
            { type: 'stretch', label: '🧘 Stretch' },
          ].map(({ type, label }) => (
            <CommandButton key={type} robotId={robot.robot_id} type={type} label={label} params={{ instruction: type }} />
          ))}
        </div>
      </div>

      {/* Movement */}
      <div style={{ marginBottom: 6 }}>
        <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 4 }}>Movement</div>
        <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          <CommandButton robotId={robot.robot_id} type="move" label="Forward" params={{ x: robot.pos_x + Math.cos(0) * 3, y: robot.pos_y + Math.sin(0) * 3 }} />
          <CommandButton robotId={robot.robot_id} type="move" label="Left" params={{ x: robot.pos_x - 3, y: robot.pos_y }} />
          <CommandButton robotId={robot.robot_id} type="move" label="Right" params={{ x: robot.pos_x + 3, y: robot.pos_y }} />
          <CommandButton robotId={robot.robot_id} type="move" label="Back" params={{ x: robot.pos_x, y: robot.pos_y - 3 }} />
          <CommandButton robotId={robot.robot_id} type="stop" label="Stop" params={{ emergency: false }} />
          <CommandButton robotId={robot.robot_id} type="stop" label="E-Stop" params={{ emergency: true }} danger />
        </div>
      </div>
    </div>
  );
}

// --- Inference Panel ---

function InferencePanel({ selectedRobot, onResult, result }: {
  selectedRobot: TelemetryEvent | undefined;
  onResult: (r: InferenceResult | null) => void;
  result: InferenceResult | null;
}) {
  const [instruction, setInstruction] = useState('walk forward');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const historyRef = useRef<Array<{ instruction: string; result: InferenceResult; time: string }>>([]);

  const runInference = async () => {
    setLoading(true);
    setError('');
    try {
      const res = await api.runInference('', instruction);
      if (res.ok && res.data) {
        const ir = res.data as InferenceResult;
        onResult(ir);
        historyRef.current = [
          { instruction, result: ir, time: new Date().toLocaleTimeString() },
          ...historyRef.current.slice(0, 9),
        ];
        // Send the command to actually move the robot
        if (selectedRobot) {
          await api.semanticCommand(selectedRobot.robot_id, instruction);
        }
      } else {
        setError(res.data?.error || 'Inference failed');
        onResult(null);
      }
    } catch {
      setError('Failed to reach inference service');
      onResult(null);
    }
    setLoading(false);
  };

  return (
    <div className="card">
      <div className="card-header">
        <div>
          <div className="card-title">AI Inference</div>
          <div className="card-subtitle">
            {selectedRobot ? `Target: ${selectedRobot.robot_id}` : 'Select a robot to apply actions'}
          </div>
        </div>
        {result && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span style={{ fontSize: 10, fontFamily: 'var(--font-mono)', color: 'var(--text-muted)' }}>
              {result.model_id}
            </span>
            <span style={{
              fontSize: 10, fontFamily: 'var(--font-mono)', padding: '2px 6px',
              borderRadius: 4, background: 'var(--bg-primary)',
              color: result.confidence > 0.8 ? 'var(--success)' : 'var(--warning)',
            }}>
              {(result.confidence * 100).toFixed(0)}% conf
            </span>
          </div>
        )}
      </div>

      {/* Input */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input
          value={instruction}
          onChange={(e) => setInstruction(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && !loading && runInference()}
          placeholder="e.g. walk forward, pick up object, wave..."
          style={{ flex: 1, padding: '8px 12px', fontSize: 13 }}
        />
        <button
          className="btn btn-primary"
          onClick={runInference}
          disabled={loading || !instruction.trim()}
          style={{ padding: '8px 20px', whiteSpace: 'nowrap' }}
        >
          {loading ? 'Running...' : 'Run Inference'}
        </button>
      </div>

      {/* Quick prompts */}
      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 12 }}>
        {['walk forward', 'turn left 90 degrees', 'pick up the box', 'wave hello', 'sit down', 'stand up'].map((p) => (
          <button
            key={p}
            className="btn btn-sm btn-primary"
            style={{ fontSize: 10, padding: '3px 8px', opacity: instruction === p ? 1 : 0.6 }}
            onClick={() => { setInstruction(p); }}
          >
            {p}
          </button>
        ))}
      </div>

      {error && (
        <div style={{ color: 'var(--danger)', fontSize: 12, marginBottom: 8 }}>{error}</div>
      )}

      {/* Result summary */}
      {result && (
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
          <ResultStat label="Actions" value={`${result.predicted_actions.length} joints`} />
          <ResultStat label="Horizon" value={`${result.action_horizon} steps`} />
          <ResultStat label="Diffusion" value={`${result.diffusion_steps} steps`} />
          <ResultStat label="Latency" value={`${result.latency_ms.toFixed(0)}ms`} />
          <ResultStat label="Confidence" value={`${(result.confidence * 100).toFixed(0)}%`}
            color={result.confidence > 0.8 ? 'var(--success)' : 'var(--warning)'} />
        </div>
      )}

      {/* Predicted actions table */}
      {result && (
        <div style={{ marginTop: 12, maxHeight: 200, overflow: 'auto' }}>
          <table style={{ width: '100%', fontSize: 11, fontFamily: 'var(--font-mono)', borderCollapse: 'collapse' }}>
            <thead>
              <tr style={{ color: 'var(--text-muted)', borderBottom: '1px solid var(--border)' }}>
                <th style={{ textAlign: 'left', padding: '4px 8px' }}>Joint</th>
                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Position</th>
                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Velocity</th>
                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Torque</th>
              </tr>
            </thead>
            <tbody>
              {result.predicted_actions.map((a) => (
                <tr key={a.joint} style={{ borderBottom: '1px solid var(--border)' }}>
                  <td style={{ padding: '3px 8px', color: 'var(--cyan)' }}>{a.joint}</td>
                  <td style={{ padding: '3px 8px', textAlign: 'right' }}>{a.position.toFixed(4)}</td>
                  <td style={{ padding: '3px 8px', textAlign: 'right' }}>{a.velocity.toFixed(4)}</td>
                  <td style={{ padding: '3px 8px', textAlign: 'right', color: Math.abs(a.torque) > 1 ? 'var(--warning)' : 'var(--text-primary)' }}>
                    {a.torque.toFixed(4)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function ResultStat({ label, value, color }: { label: string; value: string; color?: string }) {
  return (
    <div style={{ padding: '6px 10px', background: 'var(--bg-primary)', borderRadius: 6, flex: '1 1 auto', minWidth: 80 }}>
      <div style={{ fontSize: 9, color: 'var(--text-muted)', marginBottom: 2 }}>{label}</div>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: color || 'var(--text-primary)' }}>{value}</div>
    </div>
  );
}

// --- Joint Angle Visualizer ---

const JOINT_GROUPS = [
  { label: 'Head', joints: ['head_pan', 'head_tilt'] },
  { label: 'Left Arm', joints: ['left_shoulder_pitch', 'left_shoulder_roll', 'left_elbow'] },
  { label: 'Right Arm', joints: ['right_shoulder_pitch', 'right_shoulder_roll', 'right_elbow'] },
  { label: 'Left Leg', joints: ['left_hip_yaw', 'left_hip_roll', 'left_hip_pitch', 'left_knee', 'left_ankle_pitch', 'left_ankle_roll'] },
  { label: 'Right Leg', joints: ['right_hip_yaw', 'right_hip_roll', 'right_hip_pitch', 'right_knee', 'right_ankle_pitch', 'right_ankle_roll'] },
];

function JointVisualizer({ data, inferenceActions }: {
  data: TelemetryEvent;
  inferenceActions?: { joint: string; position: number; velocity: number; torque: number }[];
}) {
  const d = data as any;
  const joints: Record<string, number> = d.joints || {};
  const torques: Record<string, number> = d.joint_torques || {};
  const predicted: Record<string, { position: number; torque: number }> = {};
  if (inferenceActions) {
    for (const a of inferenceActions) {
      predicted[a.joint] = { position: a.position, torque: a.torque };
    }
  }

  if (Object.keys(joints).length === 0) {
    return (
      <div className="card">
        <div className="card-title">Joint States</div>
        <div style={{ color: 'var(--text-muted)', padding: 20, textAlign: 'center', fontSize: 13 }}>
          Waiting for joint data...
        </div>
      </div>
    );
  }

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Joint States — {Object.keys(joints).length} DOF</div>
        <div className="card-subtitle">sensor_msgs/JointState</div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {JOINT_GROUPS.map((group) => (
          <div key={group.label}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 4 }}>
              {group.label}
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
              {group.joints.map((name) => {
                const pos = joints[name] ?? 0;
                const torque = torques[name] ?? 0;
                const pred = predicted[name];
                // Map joint angle to bar: -pi..+pi → 0..100%
                const pct = ((pos + Math.PI) / (2 * Math.PI)) * 100;
                const barColor = Math.abs(torque) > 3 ? 'var(--warning)' : 'var(--accent)';
                // Predicted position marker
                const predPct = pred ? (((pos + pred.position) + Math.PI) / (2 * Math.PI)) * 100 : 0;
                return (
                  <div key={name} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11 }}>
                    <span style={{ fontFamily: 'var(--font-mono)', color: pred ? 'var(--cyan)' : 'var(--text-muted)', width: 140, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flexShrink: 0 }}>
                      {name.replace('left_', 'L_').replace('right_', 'R_')}
                    </span>
                    <div style={{ flex: 1, height: 8, background: 'var(--bg-primary)', borderRadius: 4, overflow: 'hidden', position: 'relative' }}>
                      {/* Center line */}
                      <div style={{ position: 'absolute', left: '50%', top: 0, bottom: 0, width: 1, background: 'var(--border)' }} />
                      {/* Current position bar */}
                      <div style={{
                        position: 'absolute',
                        left: pos >= 0 ? '50%' : `${pct}%`,
                        width: `${Math.abs(pos) / Math.PI * 50}%`,
                        top: 0, bottom: 0,
                        background: barColor, borderRadius: 2,
                        transition: 'all 0.15s',
                      }} />
                      {/* Predicted position marker */}
                      {pred && (
                        <div style={{
                          position: 'absolute',
                          left: `${Math.max(0, Math.min(100, predPct))}%`,
                          top: -2, width: 3, height: 12,
                          background: '#f59e0b', borderRadius: 1,
                          transition: 'left 0.3s',
                          boxShadow: '0 0 4px #f59e0b88',
                        }} />
                      )}
                    </div>
                    <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--cyan)', width: 55, textAlign: 'right', flexShrink: 0 }}>
                      {(pos * 180 / Math.PI).toFixed(1)}&deg;
                    </span>
                    {pred && (
                      <span style={{ fontFamily: 'var(--font-mono)', color: '#f59e0b', width: 55, textAlign: 'right', flexShrink: 0, fontSize: 9 }}>
                        +{(pred.position * 180 / Math.PI).toFixed(1)}&deg;
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Sensor Panel (Battery gauge, Foot Force, IMU) ---

function SensorPanel({ data }: { data: TelemetryEvent }) {
  const d = data as any;
  const batteryPct = Math.round(data.battery_level * 100);
  const voltage = (d.battery_voltage || 48 * data.battery_level).toFixed(1);
  const footL = d.foot_force_left || 0;
  const footR = d.foot_force_right || 0;
  const totalWeight = 55 * 9.81; // 55kg humanoid

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Battery Gauge */}
      <div className="card">
        <div className="card-header">
          <div className="card-title">Battery</div>
          <div className="card-subtitle">sensor_msgs/BatteryState</div>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
          {/* Circular gauge */}
          <div style={{ position: 'relative', width: 90, height: 90, flexShrink: 0 }}>
            <svg viewBox="0 0 100 100" style={{ transform: 'rotate(-90deg)' }}>
              <circle cx="50" cy="50" r="42" fill="none" stroke="var(--bg-primary)" strokeWidth="8" />
              <circle cx="50" cy="50" r="42" fill="none"
                stroke={batteryPct > 50 ? 'var(--success)' : batteryPct > 20 ? 'var(--warning)' : 'var(--danger)'}
                strokeWidth="8" strokeLinecap="round"
                strokeDasharray={`${batteryPct * 2.64} 264`}
              />
            </svg>
            <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column' }}>
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 18, fontWeight: 700 }}>{batteryPct}</span>
              <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>%</span>
            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12 }}>
            <div><span style={{ color: 'var(--text-muted)' }}>Voltage:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>{voltage}V</span></div>
            <div><span style={{ color: 'var(--text-muted)' }}>Current:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>{data.status === 'active' ? '-2.5A' : '+0.5A'}</span></div>
            <div><span style={{ color: 'var(--text-muted)' }}>Status:</span> <span className={`badge ${data.status}`} style={{ fontSize: 10 }}>{data.status === 'active' ? 'Discharging' : data.status === 'charging' ? 'Charging' : 'Idle'}</span></div>
            <div><span style={{ color: 'var(--text-muted)' }}>Temp:</span> <span style={{ fontFamily: 'var(--font-mono)' }}>{(d.motor_temp || 40).toFixed(0)}&deg;C</span></div>
          </div>
        </div>
      </div>

      {/* Foot Force */}
      <div className="card">
        <div className="card-header">
          <div className="card-title">Foot Force Sensors</div>
          <div className="card-subtitle">Force/Torque (N)</div>
        </div>
        <div style={{ display: 'flex', gap: 16, alignItems: 'end' }}>
          <FootBar label="Left" force={footL} max={totalWeight} />
          <FootBar label="Right" force={footR} max={totalWeight} />
          <div style={{ flex: 1, fontSize: 11, color: 'var(--text-muted)' }}>
            <div>Total: {(footL + footR).toFixed(0)} N</div>
            <div>Weight: {totalWeight.toFixed(0)} N</div>
            <div>Balance: {footL + footR > 0 ? `${((footL / (footL + footR)) * 100).toFixed(0)}% L` : '-'}</div>
          </div>
        </div>
      </div>

      {/* System Health */}
      <div className="card">
        <div className="card-header">
          <div className="card-title">System</div>
          <div className="card-subtitle">Diagnostics</div>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <MiniStat label="CPU Temp" value={`${(d.cpu_temp || 42).toFixed(0)}\u00B0C`} warn={(d.cpu_temp || 42) > 65} />
          <MiniStat label="Motor Temp" value={`${(d.motor_temp || 38).toFixed(0)}\u00B0C`} warn={(d.motor_temp || 38) > 60} />
          <MiniStat label="WiFi" value={`${d.wifi_rssi || -50} dBm`} warn={(d.wifi_rssi || -50) < -70} />
          <MiniStat label="Uptime" value={formatUptime(d.uptime_secs || 0)} />
        </div>
      </div>
    </div>
  );
}

function FootBar({ label, force, max }: { label: string; force: number; max: number }) {
  const pct = Math.min(100, (force / max) * 100);
  const color = pct > 80 ? 'var(--warning)' : 'var(--accent)';
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, width: 50 }}>
      <div style={{ width: 40, height: 80, background: 'var(--bg-primary)', borderRadius: 6, position: 'relative', overflow: 'hidden' }}>
        <div style={{
          position: 'absolute', bottom: 0, left: 0, right: 0,
          height: `${pct}%`, background: color, borderRadius: '0 0 6px 6px',
          transition: 'height 0.3s',
        }} />
      </div>
      <span style={{ fontSize: 10, fontFamily: 'var(--font-mono)', color: 'var(--cyan)' }}>{force.toFixed(0)}N</span>
      <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>{label}</span>
    </div>
  );
}

function MiniStat({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div style={{ padding: '8px 10px', background: 'var(--bg-primary)', borderRadius: 6 }}>
      <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 2 }}>{label}</div>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 13, color: warn ? 'var(--warning)' : 'var(--text-primary)' }}>{value}</div>
    </div>
  );
}

function formatUptime(secs: number): string {
  if (secs < 60) return `${secs}s`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`;
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`;
}

// --- Command Button ---

function CommandButton({ robotId, type, label, params, danger }: {
  robotId: string; type: string; label: string; params: Record<string, unknown>; danger?: boolean;
}) {
  const [state, setState] = useState<'idle' | 'sending' | 'sent'>('idle');
  const send = async () => {
    setState('sending');
    try {
      await api.sendCommand(robotId, type, params);
      setState('sent');
      setTimeout(() => setState('idle'), 1500);
    } catch { setState('idle'); }
  };
  return (
    <button className={`btn btn-sm ${danger ? 'btn-danger' : 'btn-primary'}`} onClick={send} disabled={state !== 'idle'}
      style={state === 'sent' ? { background: 'var(--success)' } : undefined}>
      {state === 'sending' ? '...' : state === 'sent' ? 'Sent!' : label}
    </button>
  );
}

// --- Telemetry Stream ---

function TelemetryStream({ events }: { events: TelemetryEvent[] }) {
  return (
    <div className="telemetry-stream">
      {events.length === 0 ? (
        <div style={{ color: 'var(--text-muted)', padding: 16, textAlign: 'center' }}>
          No telemetry events yet. Waiting for robots to connect...
        </div>
      ) : (
        [...events].reverse().map((e, i) => (
          <div className="telemetry-line" key={i}>
            <span className="robot-tag">{e.robot_id}</span>
            <span className="data">
              pos=({e.pos_x.toFixed(2)}, {e.pos_y.toFixed(2)})
              bat={Math.round(e.battery_level * 100)}%
              status={e.status}
            </span>
          </div>
        ))
      )}
    </div>
  );
}
