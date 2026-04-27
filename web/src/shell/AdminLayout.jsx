import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { api } from '../api/client'

const links = [
  ['dashboard', '仪表盘', 'dashboard'],
  ['providers', '数据源', 'database'],
  ['libraries', '媒体库', 'library'],
  ['tasks', '任务', 'tasks'],
  ['entries', '条目', 'list'],
  ['emby-proxy', 'Emby 代理', 'media'],
  ['webhooks', 'Webhook', 'webhook'],
  ['events', '事件', 'events'],
  ['settings', '设置', 'settings'],
]

function NavIcon({ name }) {
  const icons = {
    dashboard: <path d="M4 5h7v7H4zM13 5h7v4h-7zM13 11h7v8h-7zM4 14h7v5H4z" />,
    database: <path d="M5 7c0-2 3.1-3 7-3s7 1 7 3-3.1 3-7 3-7-1-7-3Zm0 4c0 2 3.1 3 7 3s7-1 7-3M5 15c0 2 3.1 3 7 3s7-1 7-3M5 7v8M19 7v8" />,
    library: <path d="M4 5h5v14H4zM10 5h5v14h-5zM16 6l4 12" />,
    tasks: <path d="M5 6h14M5 12h14M5 18h14M4 6h.01M4 12h.01M4 18h.01" />,
    list: <path d="M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01" />,
    media: <path d="M5 5h14v14H5zM9 9l6 3-6 3z" />,
    webhook: <path d="M7 8a3 3 0 1 1 3 3H8l-3 5M17 8a3 3 0 1 0-3 3h2l3 5M9 18h6" />,
    events: <path d="M12 4v4M12 16v4M4 12h4M16 12h4M6.3 6.3l2.8 2.8M14.9 14.9l2.8 2.8M17.7 6.3l-2.8 2.8M9.1 14.9l-2.8 2.8" />,
    settings: <path d="M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm0-5v3M12 18v3M4.2 7.5l2.6 1.5M17.2 15l2.6 1.5M19.8 7.5 17.2 9M6.8 15l-2.6 1.5" />,
  }

  return (
    <svg className="nav-icon" viewBox="0 0 24 24" aria-hidden="true">
      {icons[name]}
    </svg>
  )
}

export function AdminLayout() {
  const location = useLocation()
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => window.localStorage.getItem('sidebarCollapsed') === 'true')
  const [systemTimeZone, setSystemTimeZone] = useState('')

  useEffect(() => {
    let cancelled = false
    api.systemInfo()
      .then((data) => {
        if (!cancelled) setSystemTimeZone(data.system_timezone || '')
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  function handleToggleSidebar() {
    setSidebarCollapsed((current) => {
      window.localStorage.setItem('sidebarCollapsed', String(!current))
      return !current
    })
  }

  async function handleLogout() {
    await api.logout()
    window.location.href = '/admin/login'
  }

  return (
    <div className={`layout${sidebarCollapsed ? ' sidebar-collapsed' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-top">
          <div className="brand">
            <h1>NyaMedia</h1>
            <p>管理后台</p>
          </div>
          <button type="button" className="sidebar-toggle ghost-button" onClick={handleToggleSidebar} aria-label={sidebarCollapsed ? '展开菜单' : '收起菜单'}>
            {sidebarCollapsed ? '>' : '<'}
          </button>
        </div>
        <nav className="nav-list">
          {links.map(([path, label, icon]) => (
            <NavLink key={path} to={`/admin/${path}`} title={label} className={({ isActive }) => `nav-link${isActive ? ' active' : ''}`}>
              <NavIcon name={icon} />
              <span className="nav-label">{label}</span>
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="content">
        <header className="content-header">
          <div>
            <h2>{links.find(([path]) => location.pathname.includes(path))?.[1] || '管理后台'}</h2>
            <p>管理数据源、媒体库、扫描任务和运行状态。</p>
          </div>
          <button className="danger" onClick={handleLogout}>退出登录</button>
        </header>
        <Outlet context={{ systemTimeZone, setSystemTimeZone }} />
      </main>
    </div>
  )
}
