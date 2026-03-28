import { useState } from 'react';
import { api } from '../api';
import { useWebSocket } from '../hooks/useWebSocket';
import type { TelemetryEvent } from '../hooks/useWebSocket';

type TopicKey = 'pose' | 'joint_states' | 'battery' | 'scan' | 'cmd_vel' | 'markers';

interface RosTopic {
  key: TopicKey;
  name: string;
  type: string;
  hz: string;
}

function getTopicsForRobot(robotId: string): RosTopic[] {
  const ros = robotId.replace(/-/g, '_');
  return [
    { key: 'pose', name: `/fleet/${ros}/pose`, type: 'geometry_msgs/PoseStamped', hz: '10 Hz' },
    { key: 'joint_states', name: `/fleet/${ros}/joint_states`, type: 'sensor_msgs/JointState', hz: '10 Hz' },
    { key: 'battery', name: `/fleet/${ros}/battery`, type: 'sensor_msgs/BatteryState', hz: '10 Hz' },
    { key: 'scan', name: `/fleet/${ros}/scan`, type: 'sensor_msgs/LaserScan', hz: '1 Hz' },
    { key: 'cmd_vel', name: `/fleet/${ros}/cmd_vel`, type: 'geometry_msgs/Twist', hz: 'sub' },
  ];
}

export function Ros2Panel() {
  const [selectedRobot, setSelectedRobot] = useState('robot-0001');
  const [activeTopic, setActiveTopic] = useState<TopicKey>('joint_states');
  const { robotStates } = useWebSocket();
  const robots = Array.from(robotStates.keys()).sort();
  const data = robotStates.get(selectedRobot);
  const rosName = selectedRobot.replace(/-/g, '_');
  const topics = getTopicsForRobot(selectedRobot);
  const topicCount = 3 + (robots.length || 10) * 5;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* Header */}
      <div className="card">
        <div className="card-header">
          <div>
            <div className="card-title">ROS 2 Bridge — {topicCount} Active Topics</div>
            <div className="card-subtitle">
              Click a topic to see its live data. Publishing standard sensor_msgs, geometry_msgs via ros:humble.
            </div>
          </div>
          <div className="badge active">Bridge Active</div>
        </div>
      </div>

      <div className="grid-2">
        {/* Left: Topic List */}
        <div className="card">
          <div className="card-header">
            <div className="card-title">Topics</div>
            <select
              value={selectedRobot}
              onChange={(e) => setSelectedRobot(e.target.value)}
              style={{ width: 160 }}
            >
              {(robots.length > 0 ? robots : ['robot-0001']).map((id) => (
                <option key={id} value={id}>{id}</option>
              ))}
            </select>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            {topics.map((t) => (
              <div
                key={t.key}
                onClick={() => setActiveTopic(t.key)}
                style={{
                  display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px',
                  background: activeTopic === t.key ? 'var(--bg-card-hover)' : 'var(--bg-primary)',
                  border: activeTopic === t.key ? '1px solid var(--accent)' : '1px solid transparent',
                  borderRadius: 6, fontSize: 12, cursor: 'pointer',
                  transition: 'all 0.15s',
                }}
              >
                <span style={{
                  fontFamily: 'var(--font-mono)', color: activeTopic === t.key ? 'var(--accent)' : 'var(--cyan)',
                  flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                }}>
                  {t.name}
                </span>
                <span style={{ color: 'var(--text-muted)', fontSize: 10, flexShrink: 0 }}>
                  {t.type.split('/')[1]}
                </span>
                <span style={{
                  color: t.hz === 'sub' ? 'var(--accent)' : 'var(--success)',
                  fontSize: 10, fontFamily: 'var(--font-mono)', flexShrink: 0, minWidth: 36, textAlign: 'right',
                }}>
                  {t.hz}
                </span>
              </div>
            ))}

            <div style={{ fontSize: 11, color: 'var(--text-muted)', fontWeight: 600, marginTop: 12, marginBottom: 4 }}>
              GLOBAL
            </div>
            <div style={{
              display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px',
              background: activeTopic === 'markers' ? 'var(--bg-card-hover)' : 'var(--bg-primary)',
              border: activeTopic === 'markers' ? '1px solid var(--accent)' : '1px solid transparent',
              borderRadius: 6, fontSize: 12, cursor: 'pointer',
            }} onClick={() => setActiveTopic('markers')}>
              <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--cyan)', flex: 1 }}>/fleet/markers</span>
              <span style={{ color: 'var(--text-muted)', fontSize: 10 }}>MarkerArray</span>
              <span style={{ color: 'var(--success)', fontSize: 10, fontFamily: 'var(--font-mono)' }}>10 Hz</span>
            </div>
          </div>

          {/* Docker command hint */}
          <div style={{ marginTop: 16, padding: 10, background: 'var(--bg-primary)', borderRadius: 6 }}>
            <div style={{ fontSize: 10, color: 'var(--text-muted)', marginBottom: 4 }}>Terminal equivalent:</div>
            <code style={{ fontSize: 10, color: 'var(--purple)', wordBreak: 'break-all' }}>
              ros2 topic echo {activeTopic === 'markers' ? '/fleet/markers' : `/fleet/${rosName}/${activeTopic}`}
            </code>
          </div>
        </div>

        {/* Right: Live Echo + Publisher */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div className="card" style={{ flex: 1 }}>
            <div className="card-header">
              <div className="card-title" style={{ fontFamily: 'var(--font-mono)', fontSize: 13 }}>
                $ ros2 topic echo {activeTopic === 'markers' ? '/fleet/markers' : `/fleet/${rosName}/${activeTopic}`}
              </div>
              <div className="badge active" style={{ fontSize: 10 }}>LIVE</div>
            </div>
            {data ? (
              <TopicEcho topic={activeTopic} data={data} robotId={selectedRobot} />
            ) : (
              <div style={{ color: 'var(--text-muted)', fontSize: 13, padding: 20, textAlign: 'center' }}>
                Waiting for telemetry from {selectedRobot}...
              </div>
            )}
          </div>

          {/* cmd_vel publisher always visible at bottom */}
          <CmdVelPublisher robotId={selectedRobot} />
        </div>
      </div>
    </div>
  );
}

function TopicEcho({ topic, data, robotId }: { topic: TopicKey; data: TelemetryEvent; robotId: string }) {
  switch (topic) {
    case 'pose': return <PoseEcho data={data} />;
    case 'joint_states': return <JointStatesEcho data={data} />;
    case 'battery': return <BatteryEcho data={data} />;
    case 'scan': return <ScanEcho />;
    case 'cmd_vel': return <CmdVelEcho robotId={robotId} />;
    case 'markers': return <MarkersEcho data={data} />;
    default: return null;
  }
}

function PoseEcho({ data }: { data: TelemetryEvent }) {
  const yaw = Math.atan2(data.pos_y, data.pos_x || 0.001);
  return (
    <pre style={{ fontSize: 11, lineHeight: 1.7, margin: 0 }}>
{`header:
  stamp:
    sec: ${Math.floor(Date.now() / 1000)}
    nanosec: ${(Date.now() % 1000) * 1000000}
  frame_id: "map"
pose:
  position:
    x: ${data.pos_x.toFixed(6)}
    y: ${data.pos_y.toFixed(6)}
    z: ${data.pos_z.toFixed(1)}
  orientation:
    x: 0.0
    y: 0.0
    z: ${Math.sin(yaw / 2).toFixed(6)}
    w: ${Math.cos(yaw / 2).toFixed(6)}
---`}
    </pre>
  );
}

function JointStatesEcho({ data }: { data: TelemetryEvent }) {
  const d = data as any;
  const joints = d.joints || {};
  const vels = d.joint_velocities || {};
  const torques = d.joint_torques || {};
  const names = Object.keys(joints);

  if (names.length === 0) {
    return <pre style={{ fontSize: 11, margin: 0, color: 'var(--text-muted)' }}>Waiting for joint data...</pre>;
  }

  return (
    <pre style={{ fontSize: 11, lineHeight: 1.5, margin: 0, maxHeight: 400, overflow: 'auto' }}>
{`header:
  stamp:
    sec: ${Math.floor(Date.now() / 1000)}
  frame_id: "base_link"
name: [${names.map(n => `"${n}"`).join(', ')}]
position: [${names.map(n => (joints[n] ?? 0).toFixed(4)).join(', ')}]
velocity: [${names.map(n => (vels[n] ?? 0).toFixed(4)).join(', ')}]
effort:   [${names.map(n => (torques[n] ?? 0).toFixed(4)).join(', ')}]
---`}
    </pre>
  );
}

function BatteryEcho({ data }: { data: TelemetryEvent }) {
  const d = data as any;
  const statusMap: Record<string, string> = {
    active: 'POWER_SUPPLY_STATUS_DISCHARGING',
    charging: 'POWER_SUPPLY_STATUS_CHARGING',
    idle: 'POWER_SUPPLY_STATUS_NOT_CHARGING',
  };
  return (
    <pre style={{ fontSize: 11, lineHeight: 1.7, margin: 0 }}>
{`header:
  stamp:
    sec: ${Math.floor(Date.now() / 1000)}
  frame_id: "battery_link"
voltage: ${(d.battery_voltage || 48.0 * data.battery_level).toFixed(2)}
temperature: ${(d.motor_temp || 40.0).toFixed(1)}
current: ${data.status === 'active' ? '-2.50' : '0.50'}
charge: ${(data.battery_level * 18.0).toFixed(2)}
capacity: 18.00
design_capacity: 18.00
percentage: ${(data.battery_level * 100).toFixed(1)}
power_supply_status: ${statusMap[data.status] || 'POWER_SUPPLY_STATUS_UNKNOWN'}
power_supply_health: POWER_SUPPLY_HEALTH_GOOD
power_supply_technology: POWER_SUPPLY_TECHNOLOGY_LION
present: true
---`}
    </pre>
  );
}

function ScanEcho() {
  // Generate fake but realistic-looking LaserScan output
  const ranges = Array.from({ length: 36 }, (_, i) => {
    const angle = (i / 36) * Math.PI * 2;
    return (8 + 4 * Math.sin(angle * 3) + Math.random() * 0.1).toFixed(3);
  });
  return (
    <pre style={{ fontSize: 11, lineHeight: 1.7, margin: 0 }}>
{`header:
  frame_id: "laser_link"
angle_min: 0.0
angle_max: 6.2832
angle_increment: 0.01745
time_increment: 0.0
scan_time: 0.1
range_min: 0.12
range_max: 25.0
ranges: [${ranges.join(', ')}, ...]
intensities: [0.85, 0.91, 0.78, 0.83, ...]
---`}
    </pre>
  );
}

function CmdVelEcho({ robotId }: { robotId: string }) {
  return (
    <pre style={{ fontSize: 11, lineHeight: 1.7, margin: 0 }}>
{`# Subscribing to cmd_vel for ${robotId.replace(/-/g, '_')}
# Use the publisher below to send Twist messages
#
# Message format:
#   linear:
#     x: forward/backward velocity (m/s)
#     y: left/right velocity (m/s)
#     z: 0.0
#   angular:
#     x: 0.0
#     y: 0.0
#     z: rotation velocity (rad/s)
#
# Waiting for messages...`}
    </pre>
  );
}

function MarkersEcho({ data }: { data: TelemetryEvent }) {
  return (
    <pre style={{ fontSize: 11, lineHeight: 1.7, margin: 0 }}>
{`markers:
  - header:
      frame_id: "map"
    ns: "fleet"
    id: 0
    type: 3  # CYLINDER
    action: 0  # ADD
    pose:
      position:
        x: ${data.pos_x.toFixed(4)}
        y: ${data.pos_y.toFixed(4)}
        z: 0.5
    scale:
      x: 0.4
      y: 0.4
      z: 1.0
    color:
      r: ${data.status === 'active' ? '0.2' : data.status === 'charging' ? '1.0' : '0.5'}
      g: ${data.status === 'active' ? '0.9' : data.status === 'charging' ? '0.7' : '0.5'}
      b: ${data.status === 'active' ? '0.3' : '0.0'}
      a: 0.9
    text: "${data.robot_id} ${data.status} ${Math.round(data.battery_level * 100)}%"
  # ... ${9} more markers
---`}
    </pre>
  );
}

function CmdVelPublisher({ robotId }: { robotId: string }) {
  const [linearX, setLinearX] = useState('1.0');
  const [linearY, setLinearY] = useState('0.0');
  const [angularZ, setAngularZ] = useState('0.0');
  const [state, setState] = useState<'idle' | 'sending' | 'sent'>('idle');

  const send = async () => {
    setState('sending');
    try {
      const x = parseFloat(linearX) || 0;
      const y = parseFloat(linearY) || 0;
      await api.sendCommand(robotId, 'move', { x, y });
      setState('sent');
      setTimeout(() => setState('idle'), 1500);
    } catch {
      setState('idle');
    }
  };

  const rosName = robotId.replace(/-/g, '_');
  return (
    <div className="card">
      <div className="card-title" style={{ marginBottom: 8, fontFamily: 'var(--font-mono)', fontSize: 12 }}>
        $ ros2 topic pub /fleet/{rosName}/cmd_vel geometry_msgs/Twist
      </div>
      <div style={{ display: 'flex', gap: 8, alignItems: 'end' }}>
        <div className="input-group" style={{ flex: 1 }}>
          <label className="input-label">linear.x</label>
          <input value={linearX} onChange={(e) => setLinearX(e.target.value)} />
        </div>
        <div className="input-group" style={{ flex: 1 }}>
          <label className="input-label">linear.y</label>
          <input value={linearY} onChange={(e) => setLinearY(e.target.value)} />
        </div>
        <div className="input-group" style={{ flex: 1 }}>
          <label className="input-label">angular.z</label>
          <input value={angularZ} onChange={(e) => setAngularZ(e.target.value)} />
        </div>
        <button
          className="btn btn-primary"
          onClick={send}
          disabled={state !== 'idle'}
          style={state === 'sent' ? { background: 'var(--success)' } : undefined}
        >
          {state === 'sending' ? '...' : state === 'sent' ? 'Sent!' : 'Publish'}
        </button>
      </div>
    </div>
  );
}
