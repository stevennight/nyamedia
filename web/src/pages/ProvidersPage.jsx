import { useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true }
const emptySecret = { providerId: '', type: '', value: '' }

export function ProvidersPage() {
  const [providerForm, setProviderForm] = useState(emptyProvider)
  const [secretForm, setSecretForm] = useState(emptySecret)
  const [secretMessage, setSecretMessage] = useState('')
  const providersState = useAsyncData(async () => (await api.listProviders()).items || [], [])
  const secretsState = useAsyncData(async () => {
    if (!secretForm.providerId) return []
    return (await api.listProviderSecrets(secretForm.providerId)).items || []
  }, [secretForm.providerId])

  async function handleCreateProvider(event) {
    event.preventDefault()
    await api.createProvider(providerForm)
    setProviderForm(emptyProvider)
    providersState.refresh()
  }

  async function handleSaveSecret(event) {
    event.preventDefault()
    await api.saveProviderSecret(secretForm.providerId, secretForm.type, secretForm.value)
    setSecretForm((current) => ({ ...current, value: '' }))
    setSecretMessage('Secret saved.')
    secretsState.refresh()
  }

  async function handleDeleteSecret() {
    await api.deleteProviderSecret(secretForm.providerId, secretForm.type)
    setSecretMessage('Secret deleted.')
    secretsState.refresh()
  }

  async function handleDeleteProvider() {
    if (!providerForm.id) {
      return
    }
    await api.deleteProvider(providerForm.id)
    providersState.refresh()
  }

  return (
    <div className="page-grid two-col">
      <PageSection title="Create Provider">
        <form className="form-grid" onSubmit={handleCreateProvider}>
          <input value={providerForm.id} onChange={(e) => setProviderForm({ ...providerForm, id: e.target.value })} placeholder="id" required />
          <input value={providerForm.name} onChange={(e) => setProviderForm({ ...providerForm, name: e.target.value })} placeholder="name" required />
          <input value={providerForm.root_path} onChange={(e) => setProviderForm({ ...providerForm, root_path: e.target.value })} placeholder="root path" required />
          <select value={providerForm.type} onChange={(e) => setProviderForm({ ...providerForm, type: e.target.value })}>
            <option value="local">local</option>
          </select>
          <label className="check-inline"><input type="checkbox" checked={providerForm.enabled} onChange={(e) => setProviderForm({ ...providerForm, enabled: e.target.checked })} /> enabled</label>
          <div className="button-row">
            <button type="submit">Create</button>
            <button type="button" className="danger" onClick={handleDeleteProvider}>Delete</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Providers" actions={<button onClick={providersState.refresh}>Refresh</button>}>
        <StatusBanner error={providersState.error} loading={providersState.loading}>
          <JsonBlock value={providersState.data} />
        </StatusBanner>
      </PageSection>
      <PageSection title="Provider Secrets">
        <form className="form-grid" onSubmit={handleSaveSecret}>
          <input value={secretForm.providerId} onChange={(e) => { setSecretForm({ ...secretForm, providerId: e.target.value }); setSecretMessage('') }} placeholder="provider id" required />
          <input value={secretForm.type} onChange={(e) => setSecretForm({ ...secretForm, type: e.target.value })} placeholder="secret type" required />
          <input value={secretForm.value} onChange={(e) => setSecretForm({ ...secretForm, value: e.target.value })} placeholder="secret value" required />
          <div className="button-row">
            <button type="submit">Save</button>
            <button type="button" onClick={secretsState.refresh}>Load</button>
            <button type="button" className="danger" onClick={handleDeleteSecret}>Delete</button>
          </div>
          {secretMessage ? <div className="hint">{secretMessage}</div> : null}
        </form>
      </PageSection>
      <PageSection title="Loaded Secrets">
        <StatusBanner error={secretsState.error} loading={secretsState.loading}>
          <JsonBlock value={secretsState.data} />
        </StatusBanner>
      </PageSection>
    </div>
  )
}
