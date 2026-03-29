const API_KEY = 'dev-key-001';
const BASE = '';

async function request(method: string, path: string, body?: unknown) {
  const start = performance.now();
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: {
      'Content-Type': 'application/json',
      'X-API-Key': API_KEY,
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const elapsed = Math.round(performance.now() - start);
  const data = await res.json().catch(() => null);
  return { status: res.status, data, elapsed, ok: res.ok };
}

export const api = {
  healthz: () => request('GET', '/healthz'),
  listRobots: (limit = 20, offset = 0) =>
    request('GET', `/api/v1/robots?limit=${limit}&offset=${offset}`),
  getRobot: (id: string) => request('GET', `/api/v1/robots/${id}`),
  sendCommand: (id: string, type: string, params: Record<string, unknown>) =>
    request('POST', `/api/v1/robots/${id}/command`, { type, params }),
  getTelemetry: (id: string) => request('GET', `/api/v1/robots/${id}/telemetry`),
  runInference: (image: string, instruction: string, modelId?: string) =>
    request('POST', '/api/v1/inference', { image, instruction, model_id: modelId }),
  semanticCommand: (id: string, instruction: string) =>
    request('POST', `/api/v1/robots/${id}/semantic-command`, { instruction, robot_id: id }),
  getFleetMetrics: () => request('GET', '/api/v1/fleet/metrics'),
  getUsage: () => request('GET', '/api/v1/usage'),
};

export type ApiResponse = Awaited<ReturnType<typeof api.healthz>>;
