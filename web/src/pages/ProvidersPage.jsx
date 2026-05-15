import { useEffect, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import QRCode from 'qrcode'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const defaultDownloads = { strm: true, nfo: true, images: true, subtitles: true, bif: true, mediainfo: true }
const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true, watch_enabled: true, config: { downloads: { ...defaultDownloads }, webhook: { path_prefixes: [] } } }
const emptySecret = { type: '', value: '' }

function getProviderDownloads(config) {
  return { ...defaultDownloads, ...(config?.downloads || {}) }
}

function getProviderWebhookPrefixes(config) {
  return Array.isArray(config?.webhook?.path_prefixes) ? config.webhook.path_prefixes : []
}

function withProviderDefaults(provider) {
  return {
    id: provider.id,
    type: provider.type || 'local',
    name: provider.name || '',
    root_path: provider.root_path || '',
    enabled: provider.enabled,
    watch_enabled: provider.watch_enabled,
    config: {
      ...(provider.config || {}),
      downloads: getProviderDownloads(provider.config),
      webhook: {
        ...(provider.config?.webhook || {}),
        path_prefixes: getProviderWebhookPrefixes(provider.config),
      },
    },
  }
}

function filterDirectoryItems(items, query) {
  const keyword = query.trim().toLowerCase()
  if (!keyword) {
    return items
  }
  return items.filter((item) => `${item.name || ''} ${item.path || ''}`.toLowerCase().includes(keyword))
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
  const { systemTimeZone } = useOutletContext() || {}
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
  const [cookie115Terminal, setCookie115Terminal] = useState('tv')
  const [cookie115Auth, setCookie115Auth] = useState(null)
  const [cookie115QRCodeURL, setCookie115QRCodeURL] = useState('')
  const [cookie115AuthLoading, setCookie115AuthLoading] = useState(false)
  const [directoryPickerOpen, setDirectoryPickerOpen] = useState(false)
  const [directoryState, setDirectoryState] = useState(null)
  const [directoryLoading, setDirectoryLoading] = useState(false)
  const [directoryError, setDirectoryError] = useState('')
  const [newDirectoryName, setNewDirectoryName] = useState('')
  const [directoryFilter, setDirectoryFilter] = useState('')
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
    setCookie115Terminal('tv')
    setCookie115Auth(null)
    setCookie115QRCodeURL('')
    setCookie115AuthLoading(false)
    setDirectoryPickerOpen(false)
    setDirectoryState(null)
    setDirectoryLoading(false)
    setDirectoryError('')
    setNewDirectoryName('')
    setDirectoryFilter('')
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

  useEffect(() => {
    let cancelled = false

    async function buildQRCode() {
      if (!cookie115Auth?.qr_code) {
        setCookie115QRCodeURL('')
        return
      }
      try {
        const dataUrl = await QRCode.toDataURL(cookie115Auth.qr_code, { width: 220, margin: 1 })
        if (!cancelled) {
          setCookie115QRCodeURL(dataUrl)
        }
      } catch {
        if (!cancelled) {
          setCookie115QRCodeURL('')
        }
      }
    }

    buildQRCode()
    return () => {
      cancelled = true
    }
  }, [cookie115Auth?.qr_code])

  function openCreateDialog() {
    resetDialogState()
    setDialogMode('create')
    setDialogOpen(true)
  }

  function openEditDialog(provider) {
    setProviderForm(withProviderDefaults(provider))
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

  async function loadDirectories(path = '', options = {}) {
    setDirectoryLoading(true)
    setDirectoryError('')
    try {
      const data = selectedProviderId && providerForm.type !== 'local'
        ? await api.listProviderDirectories(selectedProviderId, path, { ...options, cloudRoot: true })
        : await api.listDirectories(path)
      setDirectoryState(data)
      setNewDirectoryName('')
      setDirectoryFilter('')
    } catch (error) {
      setDirectoryError(error.message)
    } finally {
      setDirectoryLoading(false)
    }
  }

  function openDirectoryPicker() {
    if (providerForm.type !== 'local' && !selectedProviderId) {
      setMessage('请先保存数据源，再选择远程目录。')
      return
    }
    setDirectoryPickerOpen(true)
    loadDirectories(providerForm.type === 'local' ? providerForm.root_path : '/')
  }

  function closeDirectoryPicker() {
    setDirectoryPickerOpen(false)
    setDirectoryError('')
    setNewDirectoryName('')
    setDirectoryFilter('')
  }

  async function handleCreateDirectory(event) {
    event.preventDefault()
    if (!directoryState?.path || !newDirectoryName.trim()) {
      return
    }

    setDirectoryLoading(true)
    setDirectoryError('')
    try {
      const created = await api.createDirectory(directoryState.path, newDirectoryName.trim())
      await loadDirectories(created.path)
    } catch (error) {
      setDirectoryError(error.message)
      setDirectoryLoading(false)
    }
  }

  async function handleSubmitProvider(event) {
    event.preventDefault()
    setMessage('')
    try {
      if (isEditing) {
        const updated = await api.updateProvider(providerForm.id, providerForm)
        setProviderForm(withProviderDefaults(updated))
        setMessage('数据源已更新。')
      } else {
        const created = await api.createProvider(providerForm)
        setProviderForm(withProviderDefaults(created))
        setDialogMode('edit')
        setSelectedProviderId(created.id)
        setMessage('数据源已创建，可以在下方设置密钥。')
      }
      await providersState.refresh()
    } catch (error) {
      setMessage(error.message)
    }
  }

  async function handleSaveSecret(event) {
    event.preventDefault()
    setMessage('')
    try {
      await api.saveProviderSecret(selectedProviderId, secretForm.type, secretForm.value)
      setSecretForm((current) => ({ ...current, value: '' }))
      setMessage('密钥已保存。')
      await secretsState.refresh()
    } catch (error) {
      setMessage(error.message)
    }
  }

  async function handleDeleteSecret(secretType) {
    setMessage('')
    try {
      await api.deleteProviderSecret(selectedProviderId, secretType)
      if (secretForm.type === secretType) {
        setSecretForm(emptySecret)
      }
      setMessage('密钥已删除。')
      await secretsState.refresh()
    } catch (error) {
      setMessage(error.message)
    }
  }

  async function handleDeleteProvider(providerId) {
    if (!providerId) {
      return
    }
    if (!window.confirm(`删除数据源 ${providerId}？该数据源的条目和相关缓存数据会被删除；仍被映射引用的数据源不能删除。`)) {
      return
    }
    await api.deleteProvider(providerId)
    providersState.refresh()
    if (selectedProviderId === providerId) {
      closeDialog()
    }
  }

  async function pollCookie115Auth(providerId, sessionId) {
    try {
      const status = await api.getProvider115CookieAuthStatus(providerId, sessionId)
      setCookie115Auth(status)
      if (status.state === 'authorized') {
        setMessage('115 Cookie 登录成功，Cookie 和 platform 已保存到数据源密钥。')
        secretsState.refresh()
        providersState.refresh()
        setCookie115AuthLoading(false)
        return
      }
      if (['expired', 'cancelled', 'error'].includes(status.state)) {
        setMessage(status.message || '115 cookie login stopped.')
        setCookie115AuthLoading(false)
        return
      }
      window.setTimeout(() => {
        pollCookie115Auth(providerId, sessionId)
      }, 800)
    } catch (error) {
      setCookie115Auth((current) => current ? { ...current, state: 'error', message: error.message } : null)
      setMessage(error.message)
      setCookie115AuthLoading(false)
    }
  }

  async function handleStartCookie115Auth() {
    if (!selectedProviderId) {
      return
    }
    try {
      setMessage('')
      setCookie115AuthLoading(true)
      const session = await api.startProvider115CookieAuth(selectedProviderId, cookie115Terminal)
      setCookie115Auth(session)
      setCookie115Terminal(session.terminal || cookie115Terminal)
        setMessage('请使用 115 App 扫码，然后在选择的终端类型上确认登录。')
      window.setTimeout(() => {
        pollCookie115Auth(selectedProviderId, session.session_id)
      }, 300)
    } catch (error) {
      setCookie115AuthLoading(false)
      setMessage(error.message)
    }
  }

  async function pollOpen115Auth(providerId, sessionId) {
    try {
      const status = await api.getProvider115OpenAuthStatus(providerId, sessionId)
      setOpen115Auth(status)
      if (status.state === 'authorized') {
        setMessage('115open 授权成功，Token 已保存到数据源密钥。')
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
        setMessage('请使用 115 App 扫码并确认授权。')
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

  function handleWebhookPrefixesChange(value) {
    const prefixes = value.split('\n').map((item) => item.trim()).filter(Boolean)
    setProviderForm((current) => ({
      ...current,
      config: {
        ...(current.config || {}),
        webhook: {
          ...(current.config?.webhook || {}),
          path_prefixes: prefixes,
        },
      },
    }))
  }

  const downloadConfig = getProviderDownloads(providerForm.config)
  const webhookPrefixes = getProviderWebhookPrefixes(providerForm.config)
  const canBrowseProviderRoot = providerForm.type === 'local' || Boolean(selectedProviderId)
  const isRemoteDirectoryPicker = providerForm.type !== 'local'
  const directoryItems = directoryState?.items || []
  const filteredDirectoryItems = filterDirectoryItems(directoryItems, directoryFilter)

  return (
    <div className="page-grid one-col">
      <PageSection title="数据源" actions={<><button type="button" onClick={providersState.refresh}>刷新</button><button type="button" onClick={openCreateDialog}>添加数据源</button></>}>
        <StatusBanner error={providersState.error} loading={providersState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>名称</th>
                  <th>类型</th>
                  <th>根路径</th>
                  <th>状态</th>
                  <th>启用</th>
                  <th>监听</th>
                  <th>操作</th>
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
                        <button type="button" onClick={() => openEditDialog(provider)}>编辑</button>
                        <button type="button" className="danger" onClick={() => handleDeleteProvider(provider.id)}>删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {(providersState.data || []).length === 0 ? (
                  <tr><td colSpan="8" className="empty-cell">暂无数据源。</td></tr>
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
                <h2 id="provider-dialog-title">{isEditing ? '编辑数据源' : '添加数据源'}</h2>
                <p>{isEditing ? `管理数据源 ${providerForm.id} 及其密钥。` : '数据源 ID 会自动生成为 UUID。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeDialog}>关闭</button>
            </div>

            <form className="form-grid" onSubmit={handleSubmitProvider}>
              {isEditing ? <input value={providerForm.id} placeholder="ID" disabled /> : null}
              <input value={providerForm.name} onChange={(e) => setProviderForm({ ...providerForm, name: e.target.value })} placeholder="名称" required />
              <div className="path-input-row">
                <input value={providerForm.root_path} onChange={(e) => setProviderForm({ ...providerForm, root_path: e.target.value })} placeholder={providerForm.type === '115open' || providerForm.type === '115cookie' ? '/ 或 /影视' : '根路径'} required />
                {canBrowseProviderRoot ? <button type="button" className="ghost-button" onClick={openDirectoryPicker}>浏览</button> : null}
              </div>
              <select value={providerForm.type} onChange={(e) => handleProviderTypeChange(e.target.value)}>
                <option value="local">local</option>
                <option value="115cookie">115cookie</option>
                <option value="115open">115open</option>
              </select>
              <label className="check-inline"><input type="checkbox" checked={providerForm.enabled} onChange={(e) => setProviderForm({ ...providerForm, enabled: e.target.checked })} /> 启用</label>
              <label className="check-inline"><input type="checkbox" checked={providerForm.watch_enabled} disabled={providerForm.type === '115open' || providerForm.type === '115cookie'} onChange={(e) => setProviderForm({ ...providerForm, watch_enabled: e.target.checked })} /> 实时监听</label>
              {providerForm.type === '115open' ? <div className="hint">115open 使用 115 网盘完整路径；建议根路径保持 /，目前不支持实时监听。</div> : null}
              {providerForm.type === '115cookie' ? <div className="hint">115cookie 使用 115 网盘完整路径；建议根路径保持 /，目前不支持实时监听。</div> : null}
              <div className="download-config-grid">
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.strm} onChange={(e) => handleDownloadToggle('strm', e.target.checked)} /> strm</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.nfo} onChange={(e) => handleDownloadToggle('nfo', e.target.checked)} /> nfo</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.images} onChange={(e) => handleDownloadToggle('images', e.target.checked)} /> images</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.subtitles} onChange={(e) => handleDownloadToggle('subtitles', e.target.checked)} /> subtitles</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.bif} onChange={(e) => handleDownloadToggle('bif', e.target.checked)} /> bif</label>
                <label className="check-inline"><input type="checkbox" checked={downloadConfig.mediainfo} onChange={(e) => handleDownloadToggle('mediainfo', e.target.checked)} /> mediainfo.json</label>
              </div>
              <div className="hint">控制扫描任务中生成或下载哪些附属文件。</div>
              <textarea value={webhookPrefixes.join('\n')} onChange={(e) => handleWebhookPrefixesChange(e.target.value)} rows={3} placeholder={'Webhook 路径前缀，每行一个，例如：\n/115open'} />
              <div className="hint">CloudDrive2 发来的路径必须匹配这里的前缀才会绑定到该数据源；未绑定的路径会被忽略。</div>
              <div className="button-row">
                <button type="submit">{isEditing ? '保存数据源' : '创建数据源'}</button>
                {isEditing ? <button type="button" className="danger" onClick={() => handleDeleteProvider(providerForm.id)}>删除数据源</button> : null}
              </div>
            </form>

            {message ? <div className="hint top-gap">{message}</div> : null}

            <section className="modal-section">
              <div className="section-heading">
                <h3>数据源密钥</h3>
                {selectedProviderId ? <button type="button" className="ghost-button" onClick={secretsState.refresh}>刷新密钥</button> : null}
              </div>
              {selectedProviderId && providerForm.type === '115open' ? (
                <div className="top-gap">
                  <div className="section-heading">
                    <h3>115open 授权</h3>
                  </div>
                  <div className="form-grid">
                    <input value={open115ClientId} onChange={(e) => setOpen115ClientId(e.target.value)} placeholder="115 Open AppID (client_id)" />
                    <div className="button-row">
                      <button type="button" onClick={handleStart115OpenAuth} disabled={open115AuthLoading}>{open115AuthLoading ? '授权中...' : '开始扫码授权'}</button>
                    </div>
                  </div>
                  <div className="hint">如果 AppID 留空，会使用已保存的 <code>client_id</code> 密钥。</div>
                  {open115Auth ? (
                    <div className="top-gap">
                      <div className="hint">状态：{open115Auth.state}{open115Auth.message ? ` · ${open115Auth.message}` : ''}</div>
                      {open115QRCodeURL ? <img src={open115QRCodeURL} alt="115open auth qr" style={{ width: 220, height: 220, display: 'block', marginTop: 12 }} /> : null}
                      {open115Auth.qr_code ? <div className="hint top-gap">二维码内容：<code>{open115Auth.qr_code}</code></div> : null}
                      {open115Auth.access_token ? <textarea readOnly value={open115Auth.access_token} rows={3} className="top-gap" /> : null}
                      {open115Auth.refresh_token ? <textarea readOnly value={open115Auth.refresh_token} rows={3} className="top-gap" /> : null}
                    </div>
                  ) : null}
                </div>
              ) : null}
              {selectedProviderId && providerForm.type === '115cookie' ? (
                <div className="top-gap">
                  <div className="section-heading">
                    <h3>115 Cookie 登录</h3>
                  </div>
                  <div className="form-grid">
                    <select value={cookie115Terminal} onChange={(e) => setCookie115Terminal(e.target.value)}>
                      {['tv', 'alipaymini', 'wechatmini', 'qandroid', 'web', 'android', 'ios'].map((terminal) => (
                        <option key={terminal} value={terminal}>{terminal}</option>
                      ))}
                    </select>
                    <div className="button-row">
                      <button type="button" onClick={handleStartCookie115Auth} disabled={cookie115AuthLoading}>{cookie115AuthLoading ? '登录中...' : '开始扫码登录'}</button>
                    </div>
                  </div>
                  <div className="hint">推荐终端：<code>tv</code>、<code>alipaymini</code>、<code>wechatmini</code>、<code>qandroid</code>。使用相同终端类型可能会挤掉该类型的已有会话。</div>
                  {cookie115Auth ? (
                    <div className="top-gap">
                      <div className="hint">状态：{cookie115Auth.state}{cookie115Auth.message ? ` · ${cookie115Auth.message}` : ''}</div>
                      {cookie115QRCodeURL ? <img src={cookie115QRCodeURL} alt="115 cookie login qr" style={{ width: 220, height: 220, display: 'block', marginTop: 12 }} /> : null}
                      {cookie115Auth.qr_code ? <div className="hint top-gap">二维码内容：<code>{cookie115Auth.qr_code}</code></div> : null}
                      {cookie115Auth.cookie ? <textarea readOnly value={cookie115Auth.cookie} rows={3} className="top-gap" /> : null}
                    </div>
                  ) : null}
                </div>
              ) : null}
              {!selectedProviderId ? (
                <div className="hint">请先保存数据源，再添加密钥。</div>
              ) : (
                <>
                  <form className="form-grid" onSubmit={handleSaveSecret}>
                    <input value={secretForm.type} onChange={(e) => { setSecretForm({ ...secretForm, type: e.target.value }); setMessage('') }} placeholder="密钥类型" required />
                    <div className="secret-input-row">
                      <input type={showSecretValue ? 'text' : 'password'} value={secretForm.value} onChange={(e) => { setSecretForm({ ...secretForm, value: e.target.value }); setMessage('') }} placeholder="密钥值" required />
                      <button type="button" className="ghost-button" onClick={() => setShowSecretValue((current) => !current)}>{showSecretValue ? '隐藏' : '显示'}</button>
                    </div>
                    <div className="button-row">
                      <button type="submit">保存密钥</button>
                    </div>
                  </form>
                  {providerForm.type === '115open' ? <div className="hint">推荐密钥：<code>refresh_token</code>，可选 <code>access_token</code>。</div> : null}
                  {providerForm.type === '115cookie' ? <div className="hint">推荐密钥：<code>cookie</code>；扫码登录会自动记录 <code>platform</code> 终端类型，可选 <code>user_agent</code>。</div> : null}

                  <StatusBanner error={secretsState.error} loading={secretsState.loading}>
                    <div className="table-wrap top-gap">
                      <table className="data-table">
                        <thead>
                          <tr>
                            <th>类型</th>
                            <th>密钥</th>
                            <th>更新时间</th>
                            <th>操作</th>
                          </tr>
                        </thead>
                        <tbody>
                          {(secretsState.data || []).map((secret) => (
                            <tr key={secret.secret_type}>
                              <td>{secret.secret_type}</td>
                              <td className="mono-text">{secret.masked_value}</td>
                              <td>{formatLocalDateTime(secret.updated_at, systemTimeZone)}</td>
                              <td>
                                <div className="button-row">
                                  <button type="button" className="ghost-button" onClick={() => { setSecretForm({ type: secret.secret_type, value: '' }); setMessage('请输入新值来更新该密钥。') }}>编辑</button>
                                  <button type="button" className="danger" onClick={() => handleDeleteSecret(secret.secret_type)}>删除</button>
                                </div>
                              </td>
                            </tr>
                          ))}
                          {(secretsState.data || []).length === 0 ? (
                            <tr><td colSpan="4" className="empty-cell">暂无密钥。</td></tr>
                          ) : null}
                        </tbody>
                      </table>
                    </div>
                  </StatusBanner>
                </>
              )}
            </section>

            {directoryPickerOpen ? (
              <div className="modal-backdrop nested-modal" role="presentation" onClick={closeDirectoryPicker}>
                <div className="modal-card directory-picker-card" role="dialog" aria-modal="true" aria-labelledby="directory-picker-title" onClick={(event) => event.stopPropagation()}>
                  <div className="modal-header">
                    <div>
                      <h2 id="directory-picker-title">{isRemoteDirectoryPicker ? '选择远程目录' : '选择本地目录'}</h2>
                      <p>{isRemoteDirectoryPicker ? '当前浏览的是网盘完整目录。' : '当前浏览的是服务端文件系统。'}</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={closeDirectoryPicker}>关闭</button>
                  </div>

                  <div className="directory-toolbar top-gap">
                    {isRemoteDirectoryPicker ? (
                      <button type="button" className="ghost-button" onClick={() => loadDirectories('/')}>远程根目录</button>
                    ) : (
                      <select value="" onChange={(event) => event.target.value && loadDirectories(event.target.value)}>
                        <option value="">切换根目录</option>
                        {(directoryState?.roots || []).map((root) => <option key={root} value={root}>{root}</option>)}
                      </select>
                    )}
                    <button type="button" className="ghost-button" onClick={() => loadDirectories(directoryState?.parent_path)} disabled={!directoryState?.parent_path || directoryLoading}>上级目录</button>
                    <button type="button" className="ghost-button" onClick={() => loadDirectories(directoryState?.path)} disabled={!directoryState?.path || directoryLoading}>刷新</button>
                    {isRemoteDirectoryPicker ? <button type="button" className="ghost-button" onClick={() => loadDirectories(directoryState?.path, { force: true })} disabled={!directoryState?.path || directoryLoading}>强制刷新</button> : null}
                  </div>

                  <div className="directory-current mono-text top-gap">{directoryState?.path || '正在加载...'}</div>

                  {!isRemoteDirectoryPicker ? (
                    <form className="directory-toolbar top-gap" onSubmit={handleCreateDirectory}>
                      <input value={newDirectoryName} onChange={(event) => setNewDirectoryName(event.target.value)} placeholder="新建目录名称" />
                      <button type="submit" disabled={!directoryState?.path || directoryLoading}>新建目录</button>
                    </form>
                  ) : null}

                  {directoryError ? <div className="banner banner-error top-gap">{directoryError}</div> : null}
                  {directoryLoading ? <div className="hint top-gap">正在读取目录...</div> : null}

                  <div className="directory-filter top-gap">
                    <input value={directoryFilter} onChange={(event) => setDirectoryFilter(event.target.value)} placeholder="搜索当前目录下的子目录" />
                  </div>

                  <div className="directory-list top-gap">
                    {filteredDirectoryItems.map((item) => (
                      <button type="button" className="directory-item" key={item.path} onClick={() => loadDirectories(item.path)}>
                        <span>{item.name}</span>
                        <code>{item.path}</code>
                      </button>
                    ))}
                    {!directoryLoading && directoryItems.length === 0 ? <div className="empty-cell">当前目录下没有子目录。</div> : null}
                    {!directoryLoading && directoryItems.length > 0 && filteredDirectoryItems.length === 0 ? <div className="empty-cell">没有匹配的子目录。</div> : null}
                  </div>

                  <div className="button-row top-gap">
                    <button type="button" onClick={() => { setProviderForm({ ...providerForm, root_path: directoryState?.path || providerForm.root_path }); closeDirectoryPicker() }} disabled={!directoryState?.path}>选择当前目录</button>
                  </div>
                </div>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  )
}
