import { useState } from 'react';
import type { ApiResponse } from '../api';

interface Endpoint {
  method: string;
  path: string;
  description: string;
  hasBody: boolean;
  defaultBody?: string;
  pathParams?: string[];
}

const ENDPOINTS: Endpoint[] = [
  { method: 'GET', path: '/healthz', description: 'Health check', hasBody: false },
  { method: 'GET', path: '/api/v1/robots', description: 'List all robots', hasBody: false },
  { method: 'GET', path: '/api/v1/robots/{id}', description: 'Get robot by ID', hasBody: false, pathParams: ['id'] },
  {
    method: 'POST', path: '/api/v1/robots/{id}/command', description: 'Send command to robot',
    hasBody: true, pathParams: ['id'],
    defaultBody: JSON.stringify({ type: 'move', params: { x: 5.0, y: 3.0 } }, null, 2),
  },
  { method: 'GET', path: '/api/v1/robots/{id}/telemetry', description: 'Get latest telemetry', hasBody: false, pathParams: ['id'] },
  {
    method: 'POST', path: '/api/v1/inference', description: 'Run AI inference',
    hasBody: true,
    defaultBody: JSON.stringify({ image: '', instruction: 'walk forward', model_id: 'groot-n1-v1.5' }, null, 2),
  },
  { method: 'GET', path: '/api/v1/fleet/metrics', description: 'Fleet-wide metrics', hasBody: false },
  { method: 'GET', path: '/api/v1/usage', description: 'API usage for tenant', hasBody: false },
];

export function ApiPlayground() {
  const [selected, setSelected] = useState<Endpoint>(ENDPOINTS[0]);
  const [pathParams, setPathParams] = useState<Record<string, string>>({ id: 'robot-0001' });
  const [body, setBody] = useState(selected.defaultBody ?? '');
  const [response, setResponse] = useState<ApiResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [history, setHistory] = useState<Array<{ endpoint: Endpoint; response: ApiResponse; time: string }>>([]);

  const selectEndpoint = (ep: Endpoint) => {
    setSelected(ep);
    setBody(ep.defaultBody ?? '');
    setResponse(null);
  };

  const resolvePath = (path: string) => {
    let resolved = path;
    for (const [key, value] of Object.entries(pathParams)) {
      resolved = resolved.replace(`{${key}}`, value);
    }
    return resolved;
  };

  const sendRequest = async () => {
    setLoading(true);
    try {
      const path = resolvePath(selected.path);
      let res: ApiResponse;

      if (selected.method === 'GET') {
        res = await fetch(path, {
          headers: { 'X-API-Key': 'dev-key-001' },
        }).then(async (r) => {
          const start = performance.now();
          const data = await r.json().catch(() => null);
          return { status: r.status, data, elapsed: Math.round(performance.now() - start), ok: r.ok };
        });
      } else {
        res = await fetch(path, {
          method: selected.method,
          headers: { 'Content-Type': 'application/json', 'X-API-Key': 'dev-key-001' },
          body: body || undefined,
        }).then(async (r) => {
          const data = await r.json().catch(() => null);
          return { status: r.status, data, elapsed: 0, ok: r.ok };
        });
      }

      setResponse(res);
      setHistory((prev) => [
        { endpoint: selected, response: res, time: new Date().toLocaleTimeString() },
        ...prev.slice(0, 19),
      ]);
    } catch (e) {
      setResponse({ status: 0, data: { error: e instanceof Error ? e.message : 'Request failed' }, elapsed: 0, ok: false });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="playground">
      {/* Endpoint List */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <div className="card-title">Endpoints</div>
        <div className="endpoint-list">
          {ENDPOINTS.map((ep) => (
            <div
              key={`${ep.method}-${ep.path}`}
              className={`endpoint-item ${selected === ep ? 'selected' : ''}`}
              onClick={() => selectEndpoint(ep)}
            >
              <span className={`method-badge ${ep.method}`}>{ep.method}</span>
              <div>
                <div className="endpoint-path">{ep.path}</div>
                <div className="endpoint-desc">{ep.description}</div>
              </div>
            </div>
          ))}
        </div>

        {/* History */}
        {history.length > 0 && (
          <>
            <div className="card-title" style={{ marginTop: 8 }}>History</div>
            <div className="endpoint-list" style={{ maxHeight: 200 }}>
              {history.map((h, i) => (
                <div key={i} className="endpoint-item" style={{ padding: '6px 12px' }}>
                  <span className={`method-badge ${h.endpoint.method}`} style={{ fontSize: 9 }}>{h.endpoint.method}</span>
                  <div style={{ flex: 1 }}>
                    <div className="endpoint-path" style={{ fontSize: 11 }}>{h.endpoint.path}</div>
                  </div>
                  <span className={`response-status ${h.response.ok ? 'success' : 'error'}`} style={{ fontSize: 10 }}>
                    {h.response.status}
                  </span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Request/Response Panel */}
      <div className="request-panel">
        <div className="card">
          <div className="card-header">
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <span className={`method-badge ${selected.method}`}>{selected.method}</span>
              <span className="endpoint-path">{resolvePath(selected.path)}</span>
            </div>
            <button className="btn btn-primary" onClick={sendRequest} disabled={loading}>
              {loading ? 'Sending...' : 'Send Request'}
            </button>
          </div>

          {/* Path Params */}
          {selected.pathParams && (
            <div style={{ display: 'flex', gap: 12, marginBottom: 12 }}>
              {selected.pathParams.map((p) => (
                <div className="input-group" key={p} style={{ flex: 1 }}>
                  <label className="input-label">{p}</label>
                  <input
                    value={pathParams[p] ?? ''}
                    onChange={(e) => setPathParams((prev) => ({ ...prev, [p]: e.target.value }))}
                    placeholder={`Enter ${p}`}
                  />
                </div>
              ))}
            </div>
          )}

          {/* Request Body */}
          {selected.hasBody && (
            <div className="input-group">
              <label className="input-label">Request Body (JSON)</label>
              <textarea value={body} onChange={(e) => setBody(e.target.value)} />
            </div>
          )}
        </div>

        {/* Response */}
        <div className="card" style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
          <div className="card-title" style={{ marginBottom: 8 }}>Response</div>
          {response ? (
            <>
              <div className="response-header">
                <span className={`response-status ${response.ok ? 'success' : 'error'}`}>
                  {response.status}
                </span>
                <span className="response-time">{response.elapsed}ms</span>
              </div>
              <pre style={{ flex: 1 }}>{JSON.stringify(response.data, null, 2)}</pre>
            </>
          ) : (
            <div style={{
              flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center',
              color: 'var(--text-muted)', fontSize: 13,
            }}>
              Send a request to see the response
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
