import { Navigate, useLocation } from 'react-router-dom'
import { api } from '../api/client'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const dashboardPreloadKey = 'nyamedia.dashboard.preload'

export function ProtectedLayout({ children }) {
  const location = useLocation()
  const auth = useAsyncData(async () => {
    const user = await api.me()
    const shouldPreloadDashboard = (location.pathname === '/admin' || location.pathname === '/admin/dashboard')
      && !window.sessionStorage.getItem(dashboardPreloadKey)
    if (shouldPreloadDashboard) {
      const [systemInfo, summary] = await Promise.all([
        api.systemInfo(),
        api.dashboardSummary(),
      ])
      window.sessionStorage.setItem(dashboardPreloadKey, JSON.stringify({
        systemInfo,
        providerCount: summary.provider_count ?? 0,
        libraryCount: summary.library_count ?? 0,
        taskTotal: summary.task_count ?? 0,
      }))
    }
    return user
  }, [location.pathname])

  if (!auth.loading && auth.error) {
    return <Navigate to="/admin/login" replace />
  }

  return (
    <StatusBanner error={auth.error} loading={auth.loading}>
      {children}
    </StatusBanner>
  )
}
