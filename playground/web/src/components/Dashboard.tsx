import { useState, useEffect, useCallback, useRef } from 'react';
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

      {/* Command Feed + Joint data */}
      <div className="grid-2">
        <CommandFeed robotId={selectedRobotId} />
        {selected && <JointVisualizer data={selected} />}
      </div>

      {/* Telemetry stream */}
      <TelemetryStream events={events} selectedId={selectedRobotId} />
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

interface PipelineStep {
  label: string;
  status: 'pending' | 'active' | 'done' | 'error';
  detail?: string;
  time?: number;
}

function InferencePanel({ selectedRobot }: { selectedRobot: TelemetryEvent | null }) {
  const [instruction, setInstruction] = useState('walk forward');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [steps, setSteps] = useState<PipelineStep[]>([]);

  const runInference = async () => {
    if (!selectedRobot) return;
    setLoading(true);
    setError('');

    const pipeline: PipelineStep[] = [
      { label: 'Instruction sent', status: 'active' },
      { label: 'Inference (model)', status: 'pending' },
      { label: 'Command dispatched', status: 'pending' },
      { label: 'Robot acknowledged', status: 'pending' },
    ];
    setSteps([...pipeline]);

    const t0 = performance.now();

    try {
      const res = await api.runInference('', instruction, selectedRobot.robot_id);
      const t1 = performance.now();

      pipeline[0] = { label: 'Instruction sent', status: 'done', detail: `"${instruction}"`, time: 0 };

      if (res.ok && res.data) {
        const ir = res.data as Record<string, unknown>;
        const actions = ir.predicted_actions as Array<Record<string, unknown>> | undefined;
        const conf = ((ir.confidence as number ?? 0) * 100).toFixed(0);
        const latency = ir.latency_ms as number ?? (t1 - t0);

        pipeline[1] = {
          label: 'Inference (model)',
          status: 'done',
          detail: `${ir.model_id ?? 'unknown'} | ${actions?.length ?? 0} joints | ${conf}% conf`,
          time: Math.round(latency),
        };
        setSteps([...pipeline]);

        // Now check command history to see if it was dispatched
        pipeline[2] = { label: 'Command dispatched', status: 'active' };
        setSteps([...pipeline]);

        // Poll command history for the dispatched command
        let acked = false;
        for (let attempt = 0; attempt < 15; attempt++) {
          await new Promise(r => setTimeout(r, 500));
          const cmdRes = await api.getCommandHistory(selectedRobot.robot_id, 3);
          if (cmdRes.ok && cmdRes.data) {
            const cmds = cmdRes.data as Array<Record<string, unknown>>;
            const recent = cmds[0];
            if (recent) {
              const status = recent.Status as string;
              const cmdType = recent.CommandType as string;
              const t2 = performance.now();

              if (status === 'dispatched' || status === 'acked' || status === 'timeout') {
                pipeline[2] = {
                  label: 'Command dispatched',
                  status: 'done',
                  detail: `type="${cmdType}"`,
                  time: Math.round(t2 - t1),
                };
                setSteps([...pipeline]);

                if (status === 'acked') {
                  pipeline[3] = {
                    label: 'Robot acknowledged',
                    status: 'done',
                    detail: `${cmdType} executing`,
                    time: Math.round(t2 - t0),
                  };
                  acked = true;
                  setSteps([...pipeline]);
                  break;
                } else if (status === 'timeout') {
                  pipeline[3] = { label: 'Robot acknowledged', status: 'error', detail: 'timeout' };
                  setSteps([...pipeline]);
                  break;
                }
              }
            }
          }
        }
        if (!acked && pipeline[3].status === 'pending') {
          pipeline[3] = { label: 'Robot acknowledged', status: 'active', detail: 'waiting...' };
          setSteps([...pipeline]);
        }
      } else {
        const errData = res.data as Record<string, string> | null;
        pipeline[1] = { label: 'Inference (model)', status: 'error', detail: errData?.error ?? 'failed' };
        setSteps([...pipeline]);
        setError(errData?.error || 'Inference failed');
      }
    } catch {
      pipeline[0] = { label: 'Instruction sent', status: 'error', detail: 'network error' };
      setSteps([...pipeline]);
      setError('Failed to reach inference service');
    }
    setLoading(false);
  };

  const statusColor = (s: PipelineStep['status']) => {
    switch (s) {
      case 'done': return 'var(--success)';
      case 'active': return 'var(--accent)';
      case 'error': return 'var(--danger)';
      default: return 'var(--text-muted)';
    }
  };

  const statusIcon = (s: PipelineStep['status']) => {
    switch (s) {
      case 'done': return '\u2713';
      case 'active': return '\u25CF';
      case 'error': return '\u2717';
      default: return '\u25CB';
    }
  };

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">AI Inference Pipeline</div>
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
          {loading ? 'Running...' : 'Run'}
        </button>
      </div>

      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 12 }}>
        {['walk forward', 'turn left', 'wave hello', 'dance', 'sit down', 'jump'].map((p) => (
          <button key={p} className="btn btn-sm btn-primary"
            style={{ fontSize: 10, padding: '3px 8px', opacity: instruction === p ? 1 : 0.6 }}
            onClick={() => setInstruction(p)}>
            {p}
          </button>
        ))}
      </div>

      {/* Pipeline steps visualization */}
      {steps.length > 0 && (
        <div style={{ background: 'var(--bg-primary)', borderRadius: 6, padding: '10px 12px' }}>
          {steps.map((step, i) => (
            <div key={i} style={{
              display: 'flex', alignItems: 'center', gap: 8,
              padding: '4px 0',
              fontSize: 12,
              opacity: step.status === 'pending' ? 0.4 : 1,
            }}>
              <span style={{
                color: statusColor(step.status),
                fontWeight: 700,
                width: 14,
                textAlign: 'center',
                fontSize: step.status === 'active' ? 8 : 13,
              }}>
                {statusIcon(step.status)}
              </span>
              <span style={{ color: statusColor(step.status), fontWeight: 500, minWidth: 140 }}>
                {step.label}
              </span>
              {step.detail && (
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-muted)', flex: 1 }}>
                  {step.detail}
                </span>
              )}
              {step.time !== undefined && (
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--cyan)' }}>
                  {step.time}ms
                </span>
              )}
            </div>
          ))}
        </div>
      )}

      {error && <div style={{ color: 'var(--danger)', fontSize: 12, marginTop: 8 }}>{error}</div>}
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

// --- Command Feed (live polling) ---

interface CommandEntry {
  ID: number;
  CommandID: string;
  RobotID: string;
  CommandType: string;
  Status: string;
  Instruction: string;
  CreatedAt: string;
}

function CommandFeed({ robotId }: { robotId: string | null }) {
  const [commands, setCommands] = useState<CommandEntry[]>([]);
  const [newIds, setNewIds] = useState<Set<string>>(new Set());
  const prevIdsRef = useRef<Set<string>>(new Set());

  const poll = useCallback(async () => {
    if (!robotId) return;
    const res = await api.getCommandHistory(robotId, 15);
    if (res.ok && res.data) {
      const cmds = res.data as CommandEntry[];
      // Detect newly arrived commands for flash animation
      const currentIds = new Set(cmds.map(c => c.CommandID));
      const fresh = new Set<string>();
      for (const id of currentIds) {
        if (!prevIdsRef.current.has(id)) fresh.add(id);
      }
      prevIdsRef.current = currentIds;
      if (fresh.size > 0) setNewIds(fresh);
      setCommands(cmds);
    }
  }, [robotId]);

  useEffect(() => {
    poll();
    const timer = setInterval(poll, 1500);
    return () => clearInterval(timer);
  }, [poll]);

  // Clear flash after animation
  useEffect(() => {
    if (newIds.size === 0) return;
    const timer = setTimeout(() => setNewIds(new Set()), 2000);
    return () => clearTimeout(timer);
  }, [newIds]);

  const statusBadge = (status: string) => {
    const colors: Record<string, string> = {
      'requested': 'var(--text-muted)',
      'dispatched': 'var(--accent)',
      'acked': 'var(--success)',
      'timeout': 'var(--warning)',
      'failed': 'var(--danger)',
      'inference_failed': 'var(--danger)',
    };
    return colors[status] ?? 'var(--text-muted)';
  };

  const cmdIcon = (type: string) => {
    const icons: Record<string, string> = {
      'walk': '\uD83D\uDEB6', 'move': '\uD83D\uDEB6', 'wave': '\uD83D\uDC4B',
      'dance': '\uD83D\uDC83', 'jump': '\u2B06\uFE0F', 'sit': '\uD83E\uDE91',
      'bow': '\uD83D\uDE47', 'stop': '\u26D4', 'apply_actions': '\uD83E\uDDE0',
      'look_around': '\uD83D\uDC40', 'stretch': '\uD83E\uDDD8',
    };
    return icons[type] ?? '\u2699\uFE0F';
  };

  return (
    <div className="card">
      <div className="card-header">
        <div className="card-title">Command Feed</div>
        <div className="card-subtitle">{robotId ?? 'no robot'} — live</div>
      </div>
      <div style={{ maxHeight: 280, overflow: 'auto' }}>
        {commands.length === 0 ? (
          <div style={{ color: 'var(--text-muted)', fontSize: 13, textAlign: 'center', padding: 20 }}>
            No commands yet. Try the inference panel above.
          </div>
        ) : (
          commands.map((cmd) => {
            const isNew = newIds.has(cmd.CommandID);
            const age = Math.round((Date.now() - new Date(cmd.CreatedAt).getTime()) / 1000);
            const ageStr = age < 60 ? `${age}s ago` : age < 3600 ? `${Math.round(age / 60)}m ago` : `${Math.round(age / 3600)}h ago`;
            return (
              <div key={cmd.CommandID} style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '8px 10px',
                borderBottom: '1px solid var(--border)',
                background: isNew ? 'rgba(59, 130, 246, 0.08)' : 'transparent',
                transition: 'background 1s ease-out',
              }}>
                <span style={{ fontSize: 16, width: 24, textAlign: 'center' }}>{cmdIcon(cmd.CommandType)}</span>
                <div style={{ flex: 1 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ fontWeight: 600, fontSize: 13 }}>{cmd.CommandType}</span>
                    <span style={{
                      fontSize: 10, padding: '1px 6px', borderRadius: 8,
                      background: `${statusBadge(cmd.Status)}22`,
                      color: statusBadge(cmd.Status),
                      fontWeight: 600,
                      textTransform: 'uppercase',
                    }}>
                      {cmd.Status}
                    </span>
                  </div>
                  {cmd.Instruction && (
                    <div style={{ fontSize: 11, color: 'var(--text-muted)', fontStyle: 'italic' }}>
                      "{cmd.Instruction}"
                    </div>
                  )}
                </div>
                <span style={{ fontSize: 10, color: 'var(--text-muted)', fontFamily: 'var(--font-mono)', whiteSpace: 'nowrap' }}>
                  {ageStr}
                </span>
              </div>
            );
          })
        )}
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
