/**
 * FleetOS TypeScript SDK
 * Auto-generated from OpenAPI spec (docs/openapi.yaml)
 *
 * Usage:
 *   import { FleetOS } from './fleetos';
 *   const client = new FleetOS({ apiKey: 'your-key' });
 *   const robots = await client.listRobots();
 */

export interface FleetOSConfig {
  baseUrl?: string;
  apiKey: string;
}

export interface Robot {
  id: string;
  name: string;
  model: string;
  status: 'active' | 'idle' | 'charging' | 'error';
  pos_x: number;
  pos_y: number;
  pos_z: number;
  battery_level: number;
  last_seen: string;
  registered_at: string;
  tenant_id: string;
  joints?: Record<string, number>;
  joint_velocities?: Record<string, number>;
  joint_torques?: Record<string, number>;
}

export interface ListRobotsResponse {
  robots: Robot[];
  total: number;
  limit: number;
  offset: number;
}

export interface CommandResponse {
  command_id: number;
  status: string;
  robot_id: string;
}

export interface SemanticCommandResponse {
  command_id: number;
  robot_id: string;
  status: string;
  interpreted: { type: string; params: Record<string, unknown> };
  original: string;
}

export interface InferenceResponse {
  predicted_actions: Array<{
    joint: string;
    position: number;
    velocity: number;
    torque: number;
  }>;
  confidence: number;
  model_id: string;
  model_version: string;
  embodiment: string;
  action_horizon: number;
  action_dim: number;
  diffusion_steps: number;
  latency_ms: number;
}

export interface FleetMetrics {
  total_robots: number;
  active_robots: number;
  idle_robots: number;
  error_robots: number;
  avg_battery: number;
  timestamp: string;
}

export interface Usage {
  tenant_id: string;
  date: string;
  api_calls: number;
  inference_calls: number;
}

export class FleetOS {
  private baseUrl: string;
  private apiKey: string;

  constructor(config: FleetOSConfig) {
    this.baseUrl = config.baseUrl || 'http://localhost:8080';
    this.apiKey = config.apiKey;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        'X-API-Key': this.apiKey,
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(`FleetOS API error ${res.status}: ${(err as any).error || res.statusText}`);
    }
    return res.json() as Promise<T>;
  }

  // --- Health ---
  async health(): Promise<{ status: string }> {
    return this.request('GET', '/healthz');
  }

  // --- Robots ---
  async listRobots(limit = 20, offset = 0): Promise<ListRobotsResponse> {
    return this.request('GET', `/api/v1/robots?limit=${limit}&offset=${offset}`);
  }

  async getRobot(robotId: string): Promise<Robot> {
    return this.request('GET', `/api/v1/robots/${robotId}`);
  }

  async getTelemetry(robotId: string): Promise<{ robot_id: string; state: Robot; timestamp: string }> {
    return this.request('GET', `/api/v1/robots/${robotId}/telemetry`);
  }

  // --- Commands ---
  async sendCommand(robotId: string, type: string, params: Record<string, unknown> = {}): Promise<CommandResponse> {
    return this.request('POST', `/api/v1/robots/${robotId}/command`, { type, params });
  }

  async move(robotId: string, x: number, y: number): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'move', { x, y });
  }

  async stop(robotId: string, emergency = false): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'stop', { emergency });
  }

  async dance(robotId: string): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'dance', {});
  }

  async wave(robotId: string): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'wave', {});
  }

  async jump(robotId: string): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'jump', {});
  }

  async bow(robotId: string): Promise<CommandResponse> {
    return this.sendCommand(robotId, 'bow', {});
  }

  // --- Semantic Commands ---
  async semanticCommand(robotId: string, instruction: string): Promise<SemanticCommandResponse> {
    return this.request('POST', `/api/v1/robots/${robotId}/semantic-command`, {
      instruction,
      robot_id: robotId,
    });
  }

  // --- Inference ---
  async runInference(instruction: string, options?: {
    image?: string;
    modelId?: string;
    embodiment?: string;
  }): Promise<InferenceResponse> {
    return this.request('POST', '/api/v1/inference', {
      instruction,
      image: options?.image || '',
      model_id: options?.modelId || '',
      embodiment: options?.embodiment || '',
    });
  }

  // --- Fleet ---
  async getFleetMetrics(): Promise<FleetMetrics> {
    return this.request('GET', '/api/v1/fleet/metrics');
  }

  async getUsage(): Promise<Usage> {
    return this.request('GET', '/api/v1/usage');
  }

  // --- Swarm (convenience) ---
  async swarmCommand(robotIds: string[], type: string, params: Record<string, unknown> = {}): Promise<CommandResponse[]> {
    return Promise.all(robotIds.map(id => this.sendCommand(id, type, params)));
  }

  async swarmDance(robotIds: string[]): Promise<CommandResponse[]> {
    return this.swarmCommand(robotIds, 'dance');
  }

  async swarmStop(robotIds: string[]): Promise<CommandResponse[]> {
    return this.swarmCommand(robotIds, 'stop', { emergency: false });
  }
}
