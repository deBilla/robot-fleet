import { NavLink } from 'react-router-dom'

export default function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar-brand">
        FleetOS
        <span>Admin Console</span>
      </div>
      <ul className="sidebar-nav">
        <li>
          <NavLink to="/tenants" className={({ isActive }) => isActive ? 'active' : ''}>
            Tenants
          </NavLink>
        </li>
        <li>
          <NavLink to="/billing" className={({ isActive }) => isActive ? 'active' : ''}>
            Billing
          </NavLink>
        </li>
      </ul>
    </aside>
  )
}
