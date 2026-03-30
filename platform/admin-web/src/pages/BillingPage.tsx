import { useState, useEffect, useCallback } from 'react'
import { api, type InvoiceRecord } from '../api'

export default function BillingPage() {
  const [invoices, setInvoices] = useState<InvoiceRecord[]>([])
  const [filter, setFilter] = useState<string>('all')

  const loadInvoices = useCallback(async () => {
    const res = await api.listInvoices()
    if (res.ok && res.data) setInvoices(res.data)
  }, [])

  useEffect(() => { loadInvoices() }, [loadInvoices])

  const filtered = filter === 'all' ? invoices : invoices.filter(i => i.Status === filter)

  const totalRevenue = invoices
    .filter(i => i.Status === 'paid')
    .reduce((sum, i) => sum + i.Total, 0)

  return (
    <>
      <div className="page-header">
        <div>
          <h1 className="page-title">Billing</h1>
          <p className="page-subtitle">Invoice management and revenue overview</p>
        </div>
      </div>

      <div className="stats-grid">
        <div className="stat-card">
          <div className="stat-label">Total Invoices</div>
          <div className="stat-value">{invoices.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Paid</div>
          <div className="stat-value">{invoices.filter(i => i.Status === 'paid').length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Pending</div>
          <div className="stat-value">{invoices.filter(i => i.Status === 'draft' || i.Status === 'finalized').length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Total Revenue</div>
          <div className="stat-value">${totalRevenue.toFixed(2)}</div>
        </div>
      </div>

      <div className="card">
        <div className="card-header">
          <h3 className="card-title">Invoices</h3>
          <div style={{ display: 'flex', gap: 4 }}>
            {['all', 'draft', 'finalized', 'paid', 'failed'].map(f => (
              <button
                key={f}
                className={`btn btn-sm ${filter === f ? 'btn-primary' : ''}`}
                onClick={() => setFilter(f)}
              >
                {f}
              </button>
            ))}
          </div>
        </div>
        <div className="table-container">
          <table>
            <thead>
              <tr>
                <th>Invoice ID</th>
                <th>Tenant</th>
                <th>Period</th>
                <th>Tier</th>
                <th>Total</th>
                <th>Status</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {filtered.length === 0 ? (
                <tr><td colSpan={7} className="empty-state">No invoices found</td></tr>
              ) : filtered.map(inv => (
                <tr key={inv.ID}>
                  <td className="mono">{inv.ID}</td>
                  <td className="mono text-sm">{inv.TenantID}</td>
                  <td className="text-sm">
                    {inv.PeriodStart ? new Date(inv.PeriodStart).toLocaleDateString() : '—'}
                    {' - '}
                    {inv.PeriodEnd ? new Date(inv.PeriodEnd).toLocaleDateString() : '—'}
                  </td>
                  <td><span className={`badge badge-${inv.Tier}`}>{inv.Tier}</span></td>
                  <td>${inv.Total.toFixed(2)}</td>
                  <td><span className={`badge badge-${inv.Status}`}>{inv.Status}</span></td>
                  <td className="text-sm text-muted">{new Date(inv.CreatedAt).toLocaleDateString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}
