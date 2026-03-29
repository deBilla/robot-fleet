import { useEffect, useRef, useState, useCallback } from 'react';

export interface TelemetryEvent {
  robot_id: string;
  status: string;
  pos_x: number;
  pos_y: number;
  pos_z: number;
  battery_level: number;
  timestamp: number;
  joints: Record<string, number>;
  joint_torques: Record<string, number>;
  joint_velocities: Record<string, number>;
}

export function useWebSocket(robotId?: string) {
  const [connected, setConnected] = useState(false);
  const [events, setEvents] = useState<TelemetryEvent[]>([]);
  const [robotStates, setRobotStates] = useState<Map<string, TelemetryEvent>>(() => new Map());
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const connect = useCallback(() => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const qp = new URLSearchParams({ api_key: 'dev-key-001' });
    if (robotId) qp.set('robot_id', robotId);
    const url = `${proto}//${window.location.host}/api/v1/ws/telemetry?${qp.toString()}`;

    try {
      const ws = new WebSocket(url);
      wsRef.current = ws;

      ws.onopen = () => {
        setConnected(true);
      };

      ws.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data) as TelemetryEvent;
          setEvents((prev) => [...prev.slice(-200), data]);
          setRobotStates((prev) => {
            const next = new Map(prev);
            next.set(data.robot_id, data);
            return next;
          });
        } catch {
          // ignore malformed messages
        }
      };

      ws.onclose = () => {
        setConnected(false);
        reconnectTimer.current = setTimeout(connect, 3000);
      };

      ws.onerror = () => {
        ws.close();
      };
    } catch {
      reconnectTimer.current = setTimeout(connect, 3000);
    }
  }, [robotId]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
    };
  }, [connect]);

  const clearEvents = useCallback(() => setEvents([]), []);

  return { connected, events, robotStates, clearEvents };
}
