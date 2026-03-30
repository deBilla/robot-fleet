import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import TenantsPage from './pages/TenantsPage'
import TenantDetailPage from './pages/TenantDetailPage'
import BillingPage from './pages/BillingPage'

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Navigate to="/tenants" replace />} />
        <Route path="/tenants" element={<TenantsPage />} />
        <Route path="/tenants/:id" element={<TenantDetailPage />} />
        <Route path="/billing" element={<BillingPage />} />
      </Routes>
    </Layout>
  )
}
