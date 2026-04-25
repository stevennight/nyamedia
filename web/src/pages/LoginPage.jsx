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
        <h1>NyaMedia 管理后台</h1>
        <p>登录后管理数据源、媒体库和扫描任务。</p>
        <input value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="用户名" required />
        <input type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} placeholder="密码" required />
        {error ? <div className="banner banner-error">{error}</div> : null}
        <button type="submit" disabled={submitting}>{submitting ? '登录中...' : '登录'}</button>
      </form>
    </div>
  )
}
