import { Navigate } from 'react-router-dom'
import { api } from '../api/client'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

export function ProtectedLayout({ children }) {
  const auth = useAsyncData(() => api.me(), [])

  if (!auth.loading && auth.error) {
    return <Navigate to="/admin/login" replace />
  }

  return (
    <StatusBanner error={auth.error} loading={auth.loading}>
      {children}
    </StatusBanner>
  )
}
