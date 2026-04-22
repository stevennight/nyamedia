import { useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true, watch_enabled: true }
const emptySecret = { type: '', value: '' }

function formatProviderStatus(status) {
  switch (status) {
    case 'unknown':
      return '未检查'
    case 'healthy':
      return '正常'
    case 'degraded':
      return '异常降级'
    case 'error':
      return '错误'
    case 'disabled':
      return '已禁用'
    default:
      return status || '-'
  }
}

export function ProvidersPage() {
  const [dialogOpen, setDialogOpen] = useState(false)
  const [dialogMode, setDialogMode] = useState('create')
  const [providerForm, setProviderForm] = useState(emptyProvider)
  const [secretForm, setSecretForm] = useState(emptySecret)
  const [selectedProviderId, setSelectedProviderId] = useState('')
  const [message, setMessage] = useState('')
  const [showSecretValue, setShowSecretValue] = useState(false)
  const providersState = useAsyncData(async () => (await api.listProviders()).items || [], [])
  const secretsState = useAsyncData(async () => {
    if (!selectedProviderId) return []
    return (await api.listProviderSecrets(selectedProviderId)).items || []
  }, [selectedProviderId])

  const isEditing = dialogMode === 'edit'

  function resetDialogState() {
    setProviderForm(emptyProvider)
    setSecretForm(emptySecret)
    setSelectedProviderId('')
    setMessage('')
    setShowSecretValue(false)
  }

  function openCreateDialog() {
    resetDialogState()
    setDialogMode('create')
    setDialogOpen(true)
  }

  function openEditDialog(provider) {
    setProviderForm({
      id: provider.id,
      type: provider.type || 'local',
      name: provider.name || '',
      root_path: provider.root_path || '',
      enabled: provider.enabled,
      watch_enabled: provider.watch_enabled,
    })
    setSecretForm(emptySecret)
    setSelectedProviderId(provider.id)
    setMessage('')
    setShowSecretValue(false)
    setDialogMode('edit')
    setDialogOpen(true)
  }

  function closeDialog() {
    setDialogOpen(false)
    resetDialogState()
  }

  function handleProviderTypeChange(type) {
    setProviderForm((current) => ({
      ...current,
      type,
      watch_enabled: type === '115open' ? false : current.watch_enabled,
    }))
  }

  async function handleSubmitProvider(event) {
    event.preventDefault()
    if (isEditing) {
      const updated = await api.updateProvider(providerForm.id, providerForm)
      setProviderForm({
        id: updated.id,
        type: updated.type || 'local',
        name: updated.name || '',
        root_path: updated.root_path || '',
        enabled: updated.enabled,
        watch_enabled: updated.watch_enabled,
      })
      setMessage('Provider updated.')
    } else {
      const created = await api.createProvider(providerForm)
      setProviderForm({
        id: created.id,
        type: created.type || 'local',
        name: created.name || '',
        root_path: created.root_path || '',
        enabled: created.enabled,
        watch_enabled: created.watch_enabled,
      })
      setDialogMode('edit')
      setSelectedProviderId(created.id)
      setMessage('Provider created. You can set secrets below.')
    }
    providersState.refresh()
  }

  async function handleSaveSecret(event) {
    event.preventDefault()
    await api.saveProviderSecret(selectedProviderId, secretForm.type, secretForm.value)
    setSecretForm((current) => ({ ...current, value: '' }))
    setMessage('Secret saved.')
    secretsState.refresh()
  }

  async function handleDeleteSecret(secretType) {
    await api.deleteProviderSecret(selectedProviderId, secretType)
    if (secretForm.type === secretType) {
      setSecretForm(emptySecret)
    }
    setMessage('Secret deleted.')
    secretsState.refresh()
  }

  async function handleDeleteProvider(providerId) {
    if (!providerId) {
      return
    }
    await api.deleteProvider(providerId)
    providersState.refresh()
    if (selectedProviderId === providerId) {
      closeDialog()
    }
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="Providers" actions={<><button type="button" onClick={providersState.refresh}>Refresh</button><button type="button" onClick={openCreateDialog}>Add Provider</button></>}>
        <StatusBanner error={providersState.error} loading={providersState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Root Path</th>
                  <th>Status</th>
                  <th>Enabled</th>
                  <th>Watch</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {(providersState.data || []).map((provider) => (
                  <tr key={provider.id}>
                    <td>{provider.id}</td>
                    <td>{provider.name}</td>
                    <td>{provider.type}</td>
                    <td className="mono-text">{provider.root_path}</td>
                    <td>{formatProviderStatus(provider.status)}</td>
                    <td>{String(provider.enabled)}</td>
                    <td>{String(provider.watch_enabled)}</td>
                    <td>
                      <div className="button-row">
                        <button type="button" onClick={() => openEditDialog(provider)}>Edit</button>
                        <button type="button" className="danger" onClick={() => handleDeleteProvider(provider.id)}>Delete</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {(providersState.data || []).length === 0 ? (
                  <tr><td colSpan="8" className="empty-cell">No providers found.</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>

      {dialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeDialog}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="provider-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="provider-dialog-title">{isEditing ? 'Edit Provider' : 'Add Provider'}</h2>
                <p>{isEditing ? `Manage provider ${providerForm.id} and its secrets.` : 'Provider ID will be generated automatically as a UUID.'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeDialog}>Close</button>
            </div>

            <form className="form-grid" onSubmit={handleSubmitProvider}>
              {isEditing ? <input value={providerForm.id} placeholder="id" disabled /> : null}
              <input value={providerForm.name} onChange={(e) => setProviderForm({ ...providerForm, name: e.target.value })} placeholder="name" required />
              <input value={providerForm.root_path} onChange={(e) => setProviderForm({ ...providerForm, root_path: e.target.value })} placeholder={providerForm.type === '115open' ? '/ or /影视' : 'root path'} required />
              <select value={providerForm.type} onChange={(e) => handleProviderTypeChange(e.target.value)}>
                <option value="local">local</option>
                <option value="115open">115open</option>
              </select>
              <label className="check-inline"><input type="checkbox" checked={providerForm.enabled} onChange={(e) => setProviderForm({ ...providerForm, enabled: e.target.checked })} /> enabled</label>
              <label className="check-inline"><input type="checkbox" checked={providerForm.watch_enabled} disabled={providerForm.type === '115open'} onChange={(e) => setProviderForm({ ...providerForm, watch_enabled: e.target.checked })} /> realtime watch</label>
              {providerForm.type === '115open' ? <div className="hint">115open uses 115 absolute paths and currently does not support realtime watch.</div> : null}
              <div className="button-row">
                <button type="submit">{isEditing ? 'Save Provider' : 'Create Provider'}</button>
                {isEditing ? <button type="button" className="danger" onClick={() => handleDeleteProvider(providerForm.id)}>Delete Provider</button> : null}
              </div>
            </form>

            <section className="modal-section">
              <div className="section-heading">
                <h3>Provider Secret</h3>
                {selectedProviderId ? <button type="button" className="ghost-button" onClick={secretsState.refresh}>Refresh Secrets</button> : null}
              </div>
              {!selectedProviderId ? (
                <div className="hint">Save the provider first before adding secrets.</div>
              ) : (
                <>
                  <form className="form-grid" onSubmit={handleSaveSecret}>
                    <input value={secretForm.type} onChange={(e) => { setSecretForm({ ...secretForm, type: e.target.value }); setMessage('') }} placeholder="secret type" required />
                    <div className="secret-input-row">
                      <input type={showSecretValue ? 'text' : 'password'} value={secretForm.value} onChange={(e) => { setSecretForm({ ...secretForm, value: e.target.value }); setMessage('') }} placeholder="secret value" required />
                      <button type="button" className="ghost-button" onClick={() => setShowSecretValue((current) => !current)}>{showSecretValue ? 'Hide' : 'Show'}</button>
                    </div>
                    <div className="button-row">
                      <button type="submit">Save Secret</button>
                    </div>
                  </form>
                  {providerForm.type === '115open' ? <div className="hint">Recommended secrets: <code>refresh_token</code> and optionally <code>access_token</code>.</div> : null}

                  <StatusBanner error={secretsState.error} loading={secretsState.loading}>
                    <div className="table-wrap top-gap">
                      <table className="data-table">
                        <thead>
                          <tr>
                            <th>Type</th>
                            <th>Secret</th>
                            <th>Updated</th>
                            <th>Actions</th>
                          </tr>
                        </thead>
                        <tbody>
                          {(secretsState.data || []).map((secret) => (
                            <tr key={secret.secret_type}>
                              <td>{secret.secret_type}</td>
                              <td className="mono-text">{secret.masked_value}</td>
                              <td>{secret.updated_at || '-'}</td>
                              <td>
                                <div className="button-row">
                                  <button type="button" className="ghost-button" onClick={() => { setSecretForm({ type: secret.secret_type, value: '' }); setMessage('Enter a new value to update this secret.') }}>Edit</button>
                                  <button type="button" className="danger" onClick={() => handleDeleteSecret(secret.secret_type)}>Delete</button>
                                </div>
                              </td>
                            </tr>
                          ))}
                          {(secretsState.data || []).length === 0 ? (
                            <tr><td colSpan="4" className="empty-cell">No secrets found.</td></tr>
                          ) : null}
                        </tbody>
                      </table>
                    </div>
                  </StatusBanner>
                </>
              )}
            </section>

            {message ? <div className="hint top-gap">{message}</div> : null}
          </div>
        </div>
      ) : null}
    </div>
  )
}
