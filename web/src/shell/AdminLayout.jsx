import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { api } from '../api/client'

const links = [
  ['dashboard', 'Dashboard'],
  ['providers', 'Providers'],
  ['libraries', 'Libraries'],
  ['tasks', 'Tasks'],
  ['entries', 'Entries'],
  ['emby-proxy', 'Emby Proxy'],
  ['webhooks', 'Webhooks'],
  ['settings', 'Settings'],
]

export function AdminLayout() {
  const location = useLocation()

  async function handleLogout() {
    await api.logout()
    window.location.href = '/admin/login'
  }

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="brand">
          <h1>NyaMedia</h1>
          <p>Admin Console</p>
        </div>
        <nav className="nav-list">
          {links.map(([path, label]) => (
            <NavLink key={path} to={`/admin/${path}`} className={({ isActive }) => `nav-link${isActive ? ' active' : ''}`}>
              {label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="content">
        <header className="content-header">
          <div>
            <h2>{links.find(([path]) => location.pathname.includes(path))?.[1] || 'Admin'}</h2>
            <p>Configure sources, libraries, scans, and runtime state.</p>
          </div>
          <button className="danger" onClick={handleLogout}>Logout</button>
        </header>
        <Outlet />
      </main>
    </div>
  )
}
