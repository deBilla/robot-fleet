import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Tenant, type CreateTenantResponse } from '../api'

export default function TenantsPage() {
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [showCreate, setShowCreate] = useState(false)
  const [newKey, setNewKey] = useState<string | null>(null)
  const navigate = useNavigate()

  const loadTenants = useCallback(async () => {
    const res = await api.listTenants()
    if (res.ok && res.data) setTenants(res.data)
  }, [])

  useEffect(() => { loadTenants() }, [loadTenants])

  const handleCreate = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const form = new FormData(e.currentTarget)
    const res = await api.createTenant({
      name: form.get('name') as string,
      tier: form.get('tier') as string,
      billing_email: form.get('billing_email') as string,
    })
    if (res.ok && res.data) {
      const data = res.data as CreateTenantResponse
      setNewKey(data.api_key)
      setShowCreate(false)
      loadTenants()
    }
  }

  return (
    <>
      <div className="page-header">
        <div>
          <h1 className="page-title">Tenants</h1>
          <p className="page-subtitle">Manage developer accounts and subscriptions</p>
        </div>
        <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
          + Create Tenant
        </button>
      </div>

      <div className="stats-grid">
        <div className="stat-card">
          <div className="stat-label">Total Tenants</div>
          <div className="stat-value">{tenants.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Free</div>
          <div className="stat-value">{tenants.filter(t => t.Tier === 'free').length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Pro</div>
          <div className="stat-value">{tenants.filter(t => t.Tier === 'pro').length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Enterprise</div>
          <div className="stat-value">{tenants.filter(t => t.Tier === 'enterprise').length}</div>
        </div>
      </div>

      <div className="card">
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>ID</th>
                <th>Name</th>
                <th>Tier</th>
                <th>Email</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {tenants.length === 0 ? (
                <tr><td colSpan={5} className="empty-state">No tenants yet</td></tr>
              ) : tenants.map(t => (
                <tr key={t.ID} style={{ cursor: 'pointer' }} onClick={() => navigate(`/tenants/${t.ID}`)}>
                  <td className="mono">{t.ID}</td>
                  <td>{t.Name}</td>
                  <td><span className={`badge badge-${t.Tier}`}>{t.Tier}</span></td>
                  <td className="text-muted">{t.BillingEmail || '—'}</td>
                  <td className="text-muted text-sm">{new Date(t.CreatedAt).toLocaleDateString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Create Tenant Modal */}
      {showCreate && (
        <div className="modal-overlay" onClick={() => setShowCreate(false)}>
          <div className="modal" onClick={e => e.stopPropagation()}>
            <h2 className="modal-title">Create Tenant</h2>
            <form onSubmit={handleCreate}>
              <div className="form-group">
                <label>Name</label>
                <input name="name" required placeholder="Company name" />
              </div>
              <div className="form-row">
                <div className="form-group">
                  <label>Tier</label>
                  <select name="tier" defaultValue="free">
                    <option value="free">Free</option>
                    <option value="pro">Pro</option>
                    <option value="enterprise">Enterprise</option>
                  </select>
                </div>
                <div className="form-group">
                  <label>Billing Email</label>
                  <input name="billing_email" type="email" placeholder="admin@company.com" />
                </div>
              </div>
              <div className="modal-actions">
                <button type="button" className="btn" onClick={() => setShowCreate(false)}>Cancel</button>
                <button type="submit" className="btn btn-primary">Create</button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* API Key Reveal Modal */}
      {newKey && (
        <div className="modal-overlay">
          <div className="modal">
            <h2 className="modal-title">Tenant Created</h2>
            <p className="text-sm text-muted">Here is the initial admin API key for this tenant:</p>
            <div className="key-reveal">{newKey}</div>
            <p className="key-warning">Save this key now. It cannot be retrieved again.</p>
            <div className="modal-actions">
              <button className="btn" onClick={() => { navigator.clipboard.writeText(newKey); }}>
                Copy to Clipboard
              </button>
              <button className="btn btn-primary" onClick={() => setNewKey(null)}>
                Done
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
