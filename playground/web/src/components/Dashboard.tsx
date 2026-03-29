import { useState } from 'react';
import { api } from '../api';
import { useWebSocket } from '../hooks/useWebSocket';
import type { TelemetryEvent } from '../hooks/useWebSocket';
import { SimulationView } from './SimulationView';

export function Dashboard() {
  const [selectedRobotId, setSelectedRobotId] = useState<string | null>(null);
  const [spawning, setSpawning] = useState(false);
  const { connected, events, robotStates } = useWebSocket();

  const robots = Array.from(robotStates.values());
  const selected = selectedRobotId ? robotStates.get(selectedRobotId) ?? null : robots[0] ?? null;

  // Auto-select first robot if none selected
  if (!selectedRobotId && robots.length > 0) {
    setSelectedRobotId(robots[0].robot_id);
  }

  const spawnRobot = async () => {
    setSpawning(true);
    try { await fetch('/simulator/spawn', { method: 'POST' }); } catch {}
    setSpawning(false);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* 3D View */}
      <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
        <div className="card-header" style={{ padding: '10px 16px' }}>
          <div>
            <div className="card-title">MuJoCo Physics Simulation</div>
            <div className="card-subtitle">Humanoid-v4 Lab</div>
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button className="btn btn-sm btn-primary" onClick={spawnRobot} disabled={spawning}
              style={{ padding: '5px 14px', fontSize: 12 }}>
              {spawning ? 'Spawning...' : '+ Add Robot'}
            </button>
            <div className="connection-status">
              <div className={`status-dot ${connected ? 'connected' : ''}`} />
              {connected ? 'Live' : 'Offline'}
            </div>
          </div>
        </div>
        <SimulationView robotStates={robotStates} connected={connected} />
      </div>

      {/* Robot selector + Inference */}
      <div className="grid-2">
        <RobotList
          robots={robots}
          selectedId={selectedRobotId}
          onSelect={setSelectedRobotId}
        />
        <InferencePanel selectedRobot={selected} />
      </div>

      {/* Joint data + Telemetry stream */}
      <div className="grid-2">
        {selected && <JointVisualizer data={selected} />}
        <TelemetryStream events={events} selectedId={selectedRobotId} />
      </div>
    </div>
  );
}

// --- Robot List (select target for inference) ---

function RobotList({ robots, selectedId, onSelect }: {
  robots: TelemetryEvent[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}) {
  if (robots.length === 0) {
    return (
      <div className="card" style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', minHeight: 120 }}>
        <div style={{ color: 'var(--text-muted)', fontSize: 13 }}>Waiting for robots...</div>
      </div>
    );
  }

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Robots</div>
        <div className="card-subtitle">Select target for inference</div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {robots.map((r) => {
          const isSelected = r.robot_id === selectedId;
          const bat = Math.round(r.battery_level * 100);
          return (
            <div
              key={r.robot_id}
              onClick={() => onSelect(r.robot_id)}
              style={{
                display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px',
                borderRadius: 6, cursor: 'pointer',
                background: isSelected ? 'var(--accent-bg, rgba(96,165,250,0.1))' : 'var(--bg-primary)',
                border: isSelected ? '1px solid var(--accent)' : '1px solid transparent',
                transition: 'all 0.15s',
              }}
            >
              <div className={`badge ${r.status}`} style={{ fontSize: 9 }}>{r.status}</div>
              <div style={{ flex: 1 }}>
                <div style={{ fontSize: 12, fontFamily: 'var(--font-mono)', fontWeight: isSelected ? 600 : 400 }}>
                  {r.robot_id}
                </div>
                <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
                  ({r.pos_x.toFixed(1)}, {r.pos_y.toFixed(1)})
                </div>
              </div>
              <div style={{ fontSize: 11, fontFamily: 'var(--font-mono)', color: bat > 50 ? 'var(--success)' : bat > 20 ? 'var(--warning)' : 'var(--danger)' }}>
                {bat}%
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// --- Inference Panel ---

function InferencePanel({ selectedRobot }: { selectedRobot: TelemetryEvent | null }) {
  const [instruction, setInstruction] = useState('walk forward');
  const [loading, setLoading] = useState(false);
  const [lastResult, setLastResult] = useState<string | null>(null);
  const [error, setError] = useState('');

  const runInference = async () => {
    if (!selectedRobot) return;
    setLoading(true);
    setError('');
    setLastResult(null);
    try {
      // Pass robot_id so the server forwards actions directly to the simulator
      const res = await api.runInference('', instruction, selectedRobot.robot_id);
      if (res.ok && res.data) {
        const ir = res.data as any;
        setLastResult(`${ir.model_id} | ${ir.predicted_actions?.length ?? 0} joints | ${ir.latency_ms?.toFixed(0) ?? '?'}ms | ${((ir.confidence ?? 0) * 100).toFixed(0)}% conf`);
      } else {
        setError(res.data?.error || 'Inference failed');
      }
    } catch {
      setError('Failed to reach inference service');
    }
    setLoading(false);
  };

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">AI Inference</div>
        <div className="card-subtitle">
          {selectedRobot ? selectedRobot.robot_id : 'No robot selected'}
        </div>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
        <input
          value={instruction}
          onChange={(e) => setInstruction(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && !loading && runInference()}
          placeholder="e.g. walk forward, wave hello..."
          style={{ flex: 1, padding: '8px 12px', fontSize: 13 }}
          disabled={!selectedRobot}
        />
        <button className="btn btn-primary" onClick={runInference}
          disabled={loading || !instruction.trim() || !selectedRobot}
          style={{ padding: '8px 16px', whiteSpace: 'nowrap' }}>
          {loading ? '...' : 'Run'}
        </button>
      </div>

      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
        {['walk forward', 'turn left', 'wave hello', 'dance', 'sit down', 'jump'].map((p) => (
          <button key={p} className="btn btn-sm btn-primary"
            style={{ fontSize: 10, padding: '3px 8px', opacity: instruction === p ? 1 : 0.6 }}
            onClick={() => setInstruction(p)}>
            {p}
          </button>
        ))}
      </div>

      {error && <div style={{ color: 'var(--danger)', fontSize: 12, marginTop: 8 }}>{error}</div>}
      {lastResult && (
        <div style={{ marginTop: 8, fontSize: 11, fontFamily: 'var(--font-mono)', color: 'var(--success)', padding: '6px 10px', background: 'var(--bg-primary)', borderRadius: 4 }}>
          {lastResult}
        </div>
      )}
    </div>
  );
}

// --- Joint Visualizer ---

const JOINT_GROUPS = [
  { label: 'Head', joints: ['head_pan', 'head_tilt'] },
  { label: 'Left Arm', joints: ['left_shoulder_pitch', 'left_shoulder_roll', 'left_elbow'] },
  { label: 'Right Arm', joints: ['right_shoulder_pitch', 'right_shoulder_roll', 'right_elbow'] },
  { label: 'Left Leg', joints: ['left_hip_yaw', 'left_hip_roll', 'left_hip_pitch', 'left_knee'] },
  { label: 'Right Leg', joints: ['right_hip_yaw', 'right_hip_roll', 'right_hip_pitch', 'right_knee'] },
];

function JointVisualizer({ data }: { data: TelemetryEvent }) {
  const joints: Record<string, number> = data.joints || {};
  const torques: Record<string, number> = data.joint_torques || {};

  if (Object.keys(joints).length === 0) return null;

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Joint States - {data.robot_id}</div>
        <div className="card-subtitle">{Object.keys(joints).length} DOF</div>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        {JOINT_GROUPS.map((group) => (
          <div key={group.label}>
            <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 3 }}>
              {group.label}
            </div>
            {group.joints.map((name) => {
              const pos = joints[name] ?? 0;
              const pct = ((pos + Math.PI) / (2 * Math.PI)) * 100;
              const torque = torques[name] ?? 0;
              const barColor = Math.abs(torque) > 3 ? 'var(--warning)' : 'var(--accent)';
              return (
                <div key={name} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 10, marginBottom: 2 }}>
                  <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--text-muted)', width: 110, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flexShrink: 0 }}>
                    {name.replace('left_', 'L_').replace('right_', 'R_')}
                  </span>
                  <div style={{ flex: 1, height: 6, background: 'var(--bg-primary)', borderRadius: 3, overflow: 'hidden', position: 'relative' }}>
                    <div style={{ position: 'absolute', left: '50%', top: 0, bottom: 0, width: 1, background: 'var(--border)' }} />
                    <div style={{
                      position: 'absolute',
                      left: pos >= 0 ? '50%' : `${pct}%`,
                      width: `${Math.abs(pos) / Math.PI * 50}%`,
                      top: 0, bottom: 0,
                      background: barColor, borderRadius: 2,
                      transition: 'all 0.15s',
                    }} />
                  </div>
                  <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--cyan)', width: 42, textAlign: 'right', flexShrink: 0 }}>
                    {(pos * 180 / Math.PI).toFixed(1)}
                  </span>
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Telemetry Stream ---

function TelemetryStream({ events, selectedId }: { events: TelemetryEvent[]; selectedId: string | null }) {
  const filtered = selectedId
    ? events.filter((e) => e.robot_id === selectedId)
    : events;
  const recent = filtered.slice(-40);

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Telemetry Stream</div>
        <div className="card-subtitle">{selectedId ?? 'all'} — {events.length} events</div>
      </div>
      <div style={{ maxHeight: 260, overflow: 'auto', fontFamily: 'var(--font-mono)', fontSize: 10, lineHeight: 1.6 }}>
        {recent.length === 0 ? (
          <div style={{ color: 'var(--text-muted)', padding: 16, textAlign: 'center' }}>
            Waiting for telemetry...
          </div>
        ) : (
          [...recent].reverse().map((e, i) => (
            <div key={i} style={{ padding: '2px 8px', borderBottom: '1px solid var(--border)', display: 'flex', gap: 8 }}>
              <span style={{ color: 'var(--accent)', flexShrink: 0 }}>{e.robot_id.replace('robot-', '#')}</span>
              <span style={{ color: 'var(--text-muted)' }}>
                pos=({e.pos_x.toFixed(2)}, {e.pos_y.toFixed(2)}, {(e.pos_z ?? 0).toFixed(2)})
              </span>
              <span style={{ color: e.status === 'active' ? 'var(--success)' : 'var(--warning)' }}>
                {e.status}
              </span>
              <span>bat={Math.round(e.battery_level * 100)}%</span>
              <span style={{ color: 'var(--text-muted)' }}>
                j={Object.keys(e.joints || {}).length}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
