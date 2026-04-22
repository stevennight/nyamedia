import { useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

export function SettingsPage() {
  const [form, setForm] = useState({ key: '', value: '""' })
  const settingsState = useAsyncData(async () => (await api.listSettings()).items || [], [])

  async function handleSave(event) {
    event.preventDefault()
    await api.upsertSetting(form.key, JSON.parse(form.value))
    settingsState.refresh()
  }

  async function handleDelete() {
    await api.deleteSetting(form.key)
    settingsState.refresh()
  }

  return (
    <div className="page-grid two-col">
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
