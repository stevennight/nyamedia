import { useEffect, useState } from 'react'
import QRCode from 'qrcode'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const defaultDownloads = { strm: true, nfo: true, images: true, subtitles: true, bif: true, mediainfo: true }
const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true, watch_enabled: true, config: { downloads: { ...defaultDownloads } } }
const emptySecret = { type: '', value: '' }

function getProviderDownloads(config) {
  return { ...defaultDownloads, ...(config?.downloads || {}) }
}

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
  const [open115ClientId, setOpen115ClientId] = useState('')
  const [open115Auth, setOpen115Auth] = useState(null)
  const [open115QRCodeURL, setOpen115QRCodeURL] = useState('')
  const [open115AuthLoading, setOpen115AuthLoading] = useState(false)
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
    setOpen115ClientId('')
    setOpen115Auth(null)
    setOpen115QRCodeURL('')
    setOpen115AuthLoading(false)
  }

  useEffect(() => {
    let cancelled = false

    async function buildQRCode() {
      if (!open115Auth?.qr_code) {
        setOpen115QRCodeURL('')
        return
      }
      try {
        const dataUrl = await QRCode.toDataURL(open115Auth.qr_code, { width: 220, margin: 1 })
        if (!cancelled) {
          setOpen115QRCodeURL(dataUrl)
        }
      } catch {
        if (!cancelled) {
          setOpen115QRCodeURL('')
        }
      }
    }

    buildQRCode()
    return () => {
      cancelled = true
    }
  }, [open115Auth?.qr_code])

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
      config: provider.config || { downloads: { ...defaultDownloads } },
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
      watch_enabled: type === '115open' || type === '115cookie' ? false : current.watch_enabled,
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
        config: updated.config || { downloads: { ...defaultDownloads } },
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
        config: created.config || { downloads: { ...defaultDownloads } },
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

  async function pollOpen115Auth(providerId, sessionId) {
    try {
      const status = await api.getProvider115OpenAuthStatus(providerId, sessionId)
      setOpen115Auth(status)
      if (status.state === 'authorized') {
        setMessage('115open authorization succeeded. Tokens were saved to provider secrets.')
        secretsState.refresh()
        providersState.refresh()
        setOpen115AuthLoading(false)
        return
      }
      if (['expired', 'cancelled', 'error'].includes(status.state)) {
        setMessage(status.message || '115open authorization stopped.')
        setOpen115AuthLoading(false)
        return
      }
      window.setTimeout(() => {
        pollOpen115Auth(providerId, sessionId)
      }, 800)
    } catch (error) {
      setOpen115Auth((current) => current ? { ...current, state: 'error', message: error.message } : null)
      setMessage(error.message)
      setOpen115AuthLoading(false)
    }
  }

  async function handleStart115OpenAuth() {
    if (!selectedProviderId) {
      return
    }
    try {
      setMessage('')
      setOpen115AuthLoading(true)
      const session = await api.startProvider115OpenAuth(selectedProviderId, open115ClientId)
      setOpen115Auth(session)
      setOpen115ClientId(session.client_id || open115ClientId)
      setMessage('Scan the QR code with the 115 app, then confirm authorization.')
      window.setTimeout(() => {
        pollOpen115Auth(selectedProviderId, session.session_id)
      }, 300)
    } catch (error) {
      setOpen115AuthLoading(false)
      setMessage(error.message)
    }
  }

  function handleDownloadToggle(key, checked) {
    setProviderForm((current) => ({
      ...current,
      config: {
        ...(current.config || {}),
        downloads: {
          ...getProviderDownloads(current.config),
          [key]: checked,
        },
      },
    }))
  }

  const downloadConfig = getProviderDownloads(providerForm.config)

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
              <input value={providerForm.root_path} onChange={(e) => setProviderForm({ ...providerForm, root_path: e.target.value })} placeholder={providerForm.type === '115open' || providerForm.type === '115cookie' ? '/ or /影视' : 'root path'} required />
              <select value={providerForm.type} onChange={(e) => handleProviderTypeChange(e.target.value)}>
                <option value="local">local</option>
                <option value="115cookie">115cookie</option>
                <option value="115open">115open</option>
              </select>
              <label className="check-inline"><input type="checkbox" checked={providerForm.enabled} onChange={(e) => setProviderForm({ ...providerForm, enabled: e.target.checked })} /> enabled</label>
              <label className="check-inline"><input type="checkbox" checked={providerForm.watch_enabled} disabled={providerForm.type === '115open' || providerForm.type === '115cookie'} onChange={(e) => setProviderForm({ ...providerForm, watch_enabled: e.target.checked })} /> realtime watch</label>
              {providerForm.type === '115open' ? <div className="hint">115open uses 115 absolute paths and currently does not support realtime watch.</div> : null}
              {providerForm.type === '115cookie' ? <div className="hint">115cookie uses web/client cookies and currently does not support realtime watch.</div> : null}
              <div className="download-config-grid">
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.strm} onChange={(e) => handleDownloadToggle('strm', e.target.checked)} /> strm</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.nfo} onChange={(e) => handleDownloadToggle('nfo', e.target.checked)} /> nfo</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.images} onChange={(e) => handleDownloadToggle('images', e.target.checked)} /> images</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.subtitles} onChange={(e) => handleDownloadToggle('subtitles', e.target.checked)} /> subtitles</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.bif} onChange={(e) => handleDownloadToggle('bif', e.target.checked)} /> bif</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.mediainfo} onChange={(e) => handleDownloadToggle('mediainfo', e.target.checked)} /> mediainfo.json</label>
              </div>
              <div className="hint">Controls which sidecar files are generated or downloaded during task scans.</div>
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
              {selectedProviderId && providerForm.type === '115open' ? (
                <div className="top-gap">
                  <div className="section-heading">
                    <h3>115open Auth</h3>
                  </div>
                  <div className="form-grid">
                    <input value={open115ClientId} onChange={(e) => setOpen115ClientId(e.target.value)} placeholder="115 Open AppID (client_id)" />
                    <div className="button-row">
                      <button type="button" onClick={handleStart115OpenAuth} disabled={open115AuthLoading}>{open115AuthLoading ? 'Authorizing...' : 'Start QR Auth'}</button>
                    </div>
                  </div>
                  <div className="hint">If AppID is left empty, the saved <code>client_id</code> secret will be used.</div>
                  {open115Auth ? (
                    <div className="top-gap">
                      <div className="hint">Status: {open115Auth.state}{open115Auth.message ? ` · ${open115Auth.message}` : ''}</div>
                      {open115QRCodeURL ? <img src={open115QRCodeURL} alt="115open auth qr" style={{ width: 220, height: 220, display: 'block', marginTop: 12 }} /> : null}
                      {open115Auth.qr_code ? <div className="hint top-gap">QR content: <code>{open115Auth.qr_code}</code></div> : null}
                      {open115Auth.access_token ? <textarea readOnly value={open115Auth.access_token} rows={3} className="top-gap" /> : null}
                      {open115Auth.refresh_token ? <textarea readOnly value={open115Auth.refresh_token} rows={3} className="top-gap" /> : null}
                    </div>
                  ) : null}
                </div>
              ) : null}
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
                  {providerForm.type === '115cookie' ? <div className="hint">Recommended secrets: <code>cookie</code> and optionally <code>user_agent</code>.</div> : null}

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
