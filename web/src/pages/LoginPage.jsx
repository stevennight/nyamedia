import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'

export function LoginPage() {
  const navigate = useNavigate()
  const [form, setForm] = useState({ username: '', password: '' })
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      await api.login(form)
      navigate('/admin/dashboard', { replace: true })
      window.location.reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login-shell">
      <form className="login-card" onSubmit={handleSubmit}>
        <h1>Emby115 Admin</h1>
        <p>Sign in to manage providers, libraries, and scans.</p>
        <input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="username" required />
        <input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} placeholder="password" required />
        {error ? <div className="banner banner-error">{error}</div> : null}
        <button type="submit" disabled={submitting}>{submitting ? 'Signing in...' : 'Sign In'}</button>
      </form>
    </div>
  )
}
