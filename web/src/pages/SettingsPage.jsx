import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

export function SettingsPage() {
  const [form, setForm] = useState({ key: '', value: '""' })
  const [accountForm, setAccountForm] = useState({ username: '', currentPassword: '', newPassword: '', confirmPassword: '' })
  const [accountError, setAccountError] = useState('')
  const [accountSuccess, setAccountSuccess] = useState('')
  const [accountSubmitting, setAccountSubmitting] = useState(false)
  const settingsState = useAsyncData(async () => (await api.listSettings()).items || [], [])
  const authState = useAsyncData(() => api.me(), [])

  useEffect(() => {
    if (!authState.data?.username) {
      return
    }
    setAccountForm((current) => (current.username ? current : { ...current, username: authState.data.username }))
  }, [authState.data])

  async function handleSave(event) {
    event.preventDefault()
    await api.upsertSetting(form.key, JSON.parse(form.value))
    settingsState.refresh()
  }

  async function handleDelete() {
    await api.deleteSetting(form.key)
    settingsState.refresh()
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
    <div className="page-grid two-col">
      <PageSection title="Account Security">
        <StatusBanner error={authState.error} loading={authState.loading}>
          <form className="form-grid" onSubmit={handleAccountSave}>
            <input value={accountForm.username} onChange={(e) => setAccountForm({ ...accountForm, username: e.target.value })} placeholder="username" required />
            <input type="password" value={accountForm.currentPassword} onChange={(e) => setAccountForm({ ...accountForm, currentPassword: e.target.value })} placeholder="current password" required />
            <input type="password" value={accountForm.newPassword} onChange={(e) => setAccountForm({ ...accountForm, newPassword: e.target.value })} placeholder="new password (optional)" />
            <input type="password" value={accountForm.confirmPassword} onChange={(e) => setAccountForm({ ...accountForm, confirmPassword: e.target.value })} placeholder="confirm new password" />
            {accountError ? <div className="banner banner-error">{accountError}</div> : null}
            {accountSuccess ? <div className="banner banner-success">{accountSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={accountSubmitting}>{accountSubmitting ? 'Saving...' : 'Update Account'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
      <PageSection title="Edit Setting">
        <form className="form-grid" onSubmit={handleSave}>
          <input value={form.key} onChange={(e) => setForm({ ...form, key: e.target.value })} placeholder="setting key" required />
          <textarea value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} rows="10" placeholder='JSON value, e.g. "redirect"' />
          <div className="button-row">
            <button type="submit">Save</button>
            <button type="button" className="danger" onClick={handleDelete}>Delete</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Settings" actions={<button onClick={settingsState.refresh}>Refresh</button>}>
        <StatusBanner error={settingsState.error} loading={settingsState.loading}>
          <JsonBlock value={settingsState.data} />
        </StatusBanner>
      </PageSection>
    </div>
  )
}
