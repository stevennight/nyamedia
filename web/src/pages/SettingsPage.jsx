import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { applyThemeMode, getStoredThemeMode, saveThemeMode } from '../utils/theme'

export function SettingsPage() {
  const [themeMode, setThemeMode] = useState(() => getStoredThemeMode())
  const [accountForm, setAccountForm] = useState({ username: '', currentPassword: '', newPassword: '', confirmPassword: '' })
  const [accountError, setAccountError] = useState('')
  const [accountSuccess, setAccountSuccess] = useState('')
  const [accountSubmitting, setAccountSubmitting] = useState(false)
  const authState = useAsyncData(() => api.me(), [])

  useEffect(() => {
    if (!authState.data?.username) {
      return
    }
    setAccountForm((current) => (current.username ? current : { ...current, username: authState.data.username }))
  }, [authState.data])

  useEffect(() => {
    applyThemeMode(themeMode)
    if (themeMode !== 'system') {
      return undefined
    }

    const media = window.matchMedia('(prefers-color-scheme: light)')
    const handleChange = () => applyThemeMode('system')
    media.addEventListener('change', handleChange)
    return () => media.removeEventListener('change', handleChange)
  }, [themeMode])

  function handleThemeModeChange(event) {
    const nextMode = event.target.value
    setThemeMode(nextMode)
    saveThemeMode(nextMode)
  }

  async function handleAccountSave(event) {
    event.preventDefault()
    setAccountError('')
    setAccountSuccess('')

    if (accountForm.newPassword && accountForm.newPassword !== accountForm.confirmPassword) {
      setAccountError('两次输入的新密码不一致')
      return
    }

    setAccountSubmitting(true)
    try {
      const data = await api.updateMe({
        username: accountForm.username,
        current_password: accountForm.currentPassword,
        new_password: accountForm.newPassword,
      })
      setAccountForm((current) => ({ ...current, username: data.username, currentPassword: '', newPassword: '', confirmPassword: '' }))
      setAccountSuccess('账号信息已更新')
      authState.refresh()
    } catch (err) {
      setAccountError(err instanceof Error ? err.message : String(err))
    } finally {
      setAccountSubmitting(false)
    }
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="界面外观">
        <div className="form-grid compact">
          <select value={themeMode} onChange={handleThemeModeChange} aria-label="主题模式">
            <option value="system">跟随系统</option>
            <option value="light">明亮模式</option>
            <option value="dark">黑暗模式</option>
          </select>
          <div className="hint">选择“跟随系统”时，会根据操作系统的浅色/深色偏好自动切换。</div>
        </div>
      </PageSection>
      <PageSection title="账号安全">
        <StatusBanner error={authState.error} loading={authState.loading}>
          <form className="form-grid" onSubmit={handleAccountSave}>
            <input value={accountForm.username} onChange={(e) => setAccountForm({ ...accountForm, username: e.target.value })} placeholder="用户名" required />
            <input type="password" value={accountForm.currentPassword} onChange={(e) => setAccountForm({ ...accountForm, currentPassword: e.target.value })} placeholder="当前密码" required />
            <input type="password" value={accountForm.newPassword} onChange={(e) => setAccountForm({ ...accountForm, newPassword: e.target.value })} placeholder="新密码（可选）" />
            <input type="password" value={accountForm.confirmPassword} onChange={(e) => setAccountForm({ ...accountForm, confirmPassword: e.target.value })} placeholder="确认新密码" />
            {accountError ? <div className="banner banner-error">{accountError}</div> : null}
            {accountSuccess ? <div className="banner banner-success">{accountSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={accountSubmitting}>{accountSubmitting ? '保存中...' : '更新账号'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
    </div>
  )
}
