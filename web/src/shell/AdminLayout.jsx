import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { api } from '../api/client'

const links = [
  ['dashboard', '仪表盘'],
  ['providers', '数据源'],
  ['libraries', '媒体库'],
  ['tasks', '任务'],
  ['entries', '条目'],
  ['emby-proxy', 'Emby 代理'],
  ['webhooks', 'Webhook'],
  ['settings', '设置'],
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
          <p>管理后台</p>
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
            <h2>{links.find(([path]) => location.pathname.includes(path))?.[1] || '管理后台'}</h2>
            <p>管理数据源、媒体库、扫描任务和运行状态。</p>
          </div>
          <button className="danger" onClick={handleLogout}>退出登录</button>
        </header>
        <Outlet />
      </main>
    </div>
  )
}
