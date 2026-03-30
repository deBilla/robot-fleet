import { useState, useEffect, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Tenant, type APIKeyRecord, type CreateKeyResponse } from '../api'

type Tab = 'keys' | 'billing'

export default function TenantDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [tenant, setTenant] = useState<Tenant | null>(null)
  const [keys, setKeys] = useState<APIKeyRecord[]>([])
  const [tab, setTab] = useState<Tab>('keys')
  const [showCreateKey, setShowCreateKey] = useState(false)
  const [newKey, setNewKey] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)

  const loadTenant = useCallback(async () => {
    if (!id) return
    const res = await api.getTenant(id)
    if (res.ok && res.data) setTenant(res.data)
    else navigate('/tenants')
  }, [id, navigate])

  const loadKeys = useCallback(async () => {
    if (!id) return
    const res = await api.listKeys(id)
    if (res.ok && res.data) setKeys(res.data)
  }, [id])

  useEffect(() => { loadTenant(); loadKeys() }, [loadTenant, loadKeys])

  const handleCreateKey = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    if (!id) return
    const form = new FormData(e.currentTarget)
    const res = await api.createKey(id, {
      name: form.get('name') as string,
      role: form.get('role') as string,
      rate_limit: parseInt(form.get('rate_limit') as string) || 100,
    })
    if (res.ok && res.data) {
      const data = res.data as CreateKeyResponse
      setNewKey(data.api_key)
      setShowCreateKey(false)
      loadKeys()
    }
  }

  const handleRevoke = async (hash: string) => {
    if (!confirm('Revoke this API key? This cannot be undone.')) return
    const res = await api.revokeKey(hash)
    if (res.ok) loadKeys()
  }

  const handleUpdate = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    if (!id) return
    const form = new FormData(e.currentTarget)
    await api.updateTenant(id, {
      name: form.get('name') as string,
      billing_email: form.get('billing_email') as string,
    })
    setEditing(false)
    loadTenant()
  }

  if (!tenant) return null

  return (
    <>
      <div className="page-header">
        <div>
          <p className="text-sm text-muted" style={{ cursor: 'pointer' }} onClick={() => navigate('/tenants')}>
            &larr; Back to Tenants
          </p>
          <h1 className="page-title">{tenant.Name}</h1>
          <p className="page-subtitle mono">{tenant.ID}</p>
        </div>
        <span className={`badge badge-${tenant.Tier}`}>{tenant.Tier}</span>
      </div>

      {/* Tenant Info Card */}
      <div className="card">
        <div className="card-header">
          <h3 className="card-title">Tenant Details</h3>
          {!editing && (
            <button className="btn btn-sm" onClick={() => setEditing(true)}>Edit</button>
          )}
        </div>
        {editing ? (
          <form onSubmit={handleUpdate}>
            <div className="form-row">
              <div className="form-group">
                <label>Name</label>
                <input name="name" defaultValue={tenant.Name} required />
              </div>
              <div className="form-group">
                <label>Billing Email</label>
                <input name="billing_email" type="email" defaultValue={tenant.BillingEmail} />
              </div>
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button type="submit" className="btn btn-primary btn-sm">Save</button>
              <button type="button" className="btn btn-sm" onClick={() => setEditing(false)}>Cancel</button>
            </div>
          </form>
        ) : (
          <div className="form-row">
            <div>
              <div className="stat-label">Email</div>
              <div className="mt-2">{tenant.BillingEmail || '—'}</div>
            </div>
            <div>
              <div className="stat-label">Created</div>
              <div className="mt-2">{new Date(tenant.CreatedAt).toLocaleString()}</div>
            </div>
          </div>
        )}
      </div>

      {/* Tabs */}
      <div className="tabs">
        <button className={`tab ${tab === 'keys' ? 'active' : ''}`} onClick={() => setTab('keys')}>
          API Keys ({keys.length})
        </button>
        <button className={`tab ${tab === 'billing' ? 'active' : ''}`} onClick={() => setTab('billing')}>
          Billing
        </button>
      </div>

      {/* API Keys Tab */}
      {tab === 'keys' && (
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">API Keys</h3>
            <button className="btn btn-primary btn-sm" onClick={() => setShowCreateKey(true)}>
              + Create Key
            </button>
          </div>
          <div className="table-container">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Role</th>
                  <th>Rate Limit</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th>Expires</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {keys.length === 0 ? (
                  <tr><td colSpan={7} className="empty-state">No API keys</td></tr>
                ) : keys.map(k => (
                  <tr key={k.KeyHash}>
                    <td>{k.Name}</td>
                    <td><span className={`badge badge-${k.Role}`}>{k.Role}</span></td>
                    <td>{k.RateLimit} rps</td>
                    <td>
                      <span className={`badge ${k.Revoked ? 'badge-revoked' : 'badge-active'}`}>
                        {k.Revoked ? 'revoked' : 'active'}
                      </span>
                    </td>
                    <td className="text-sm text-muted">{new Date(k.CreatedAt).toLocaleDateString()}</td>
                    <td className="text-sm text-muted">
                      {k.ExpiresAt ? new Date(k.ExpiresAt).toLocaleDateString() : 'Never'}
                    </td>
                    <td>
                      {!k.Revoked && (
                        <button className="btn btn-danger btn-sm" onClick={() => handleRevoke(k.KeyHash)}>
                          Revoke
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Billing Tab */}
      {tab === 'billing' && (
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">Billing</h3>
          </div>
          <div className="stats-grid">
            <div className="stat-card">
              <div className="stat-label">Current Tier</div>
              <div className="stat-value" style={{ fontSize: 20 }}>{tenant.Tier}</div>
            </div>
          </div>
          <p className="text-muted text-sm">
            Invoice details available in the Billing page. Billing cycle managed via Temporal workflow.
          </p>
        </div>
      )}

      {/* Create Key Modal */}
      {showCreateKey && (
        <div className="modal-overlay" onClick={() => setShowCreateKey(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <h2 className="modal-title">Create API Key</h2>
            <form onSubmit={handleCreateKey}>
              <div className="form-group">
                <label>Key Name</label>
                <input name="name" required placeholder="e.g. Production API Key" />
              </div>
              <div className="form-row">
                <div className="form-group">
                  <label>Role</label>
                  <select name="role" defaultValue="developer">
                    <option value="admin">Admin</option>
                    <option value="operator">Operator</option>
                    <option value="developer">Developer</option>
                    <option value="viewer">Viewer</option>
                  </select>
                </div>
                <div className="form-group">
                  <label>Rate Limit (rps)</label>
                  <input name="rate_limit" type="number" defaultValue={100} min={1} />
                </div>
              </div>
              <div className="modal-actions">
                <button type="button" className="btn" onClick={() => setShowCreateKey(false)}>Cancel</button>
                <button type="submit" className="btn btn-primary">Create</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Key Reveal Modal */}
      {newKey && (
        <div className="modal-overlay">
          <div className="modal">
            <h2 className="modal-title">API Key Created</h2>
            <div className="key-reveal">{newKey}</div>
            <p className="key-warning">Save this key now. It cannot be retrieved again.</p>
            <div className="modal-actions">
              <button className="btn" onClick={() => navigator.clipboard.writeText(newKey)}>
                Copy to Clipboard
              </button>
              <button className="btn btn-primary" onClick={() => setNewKey(null)}>Done</button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
