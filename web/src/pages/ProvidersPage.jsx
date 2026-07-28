import { useEffect, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import QRCode from 'qrcode'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const defaultDownloads = { strm: true, nfo: true, images: true, subtitles: true, bif: true, mediainfo: true }
const defaultScanRequestIntervalMs = 500
const defaultCookie115RequestIntervalMinSeconds = 2
const defaultCookie115RequestIntervalMaxSeconds = 5
const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true, watch_enabled: true, config: { downloads: { ...defaultDownloads }, webhook: { path_prefixes: [] } } }
const emptySecret = { type: '', value: '' }
const emptyPan123Credentials = { client_id: '', client_secret: '' }

function isCloudProviderType(type) {
  return type === '115open' || type === '115cookie' || type === '123pan'
}

function supportsProviderWatch(type) {
  return type === 'local'
}

function supportsScanRequestInterval(type) {
  return type === '115open' || type === '123pan'
}

function stopAuthPolling(polling) {
  polling.generation += 1
  if (polling.timer !== null) {
    window.clearTimeout(polling.timer)
  }
  polling.controller?.abort()
  polling.timer = null
  polling.controller = null
}

function getProviderDownloads(config) {
  return { ...defaultDownloads, ...(config?.downloads || {}) }
}

function getProviderWebhookPrefixes(config) {
  return Array.isArray(config?.webhook?.path_prefixes) ? config.webhook.path_prefixes : []
}

function getScanRequestIntervalMs(config) {
  const value = Number(config?.scan_request_interval_ms)
  return Number.isFinite(value) && value > 0 ? value : defaultScanRequestIntervalMs
}

function getCookie115RequestIntervalSeconds(config, key, fallback) {
  const value = Number(config?.[key])
  return Number.isFinite(value) && value >= 1 ? value : fallback
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
      ...(supportsScanRequestInterval(provider.type) ? { scan_request_interval_ms: getScanRequestIntervalMs(provider.config) } : {}),
      ...(provider.type === '115cookie' ? {
        request_interval_min_seconds: getCookie115RequestIntervalSeconds(provider.config, 'request_interval_min_seconds', defaultCookie115RequestIntervalMinSeconds),
        request_interval_max_seconds: getCookie115RequestIntervalSeconds(provider.config, 'request_interval_max_seconds', defaultCookie115RequestIntervalMaxSeconds),
      } : {}),
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
  const [dialogTab, setDialogTab] = useState('settings')
  const [providerForm, setProviderForm] = useState(emptyProvider)
  const [secretForm, setSecretForm] = useState(emptySecret)
  const [selectedProviderId, setSelectedProviderId] = useState('')
  const [message, setMessage] = useState('')
  const [providerActionError, setProviderActionError] = useState('')
  const [showSecretValue, setShowSecretValue] = useState(false)
  const [open115ClientId, setOpen115ClientId] = useState('')
  const [open115Tokens, setOpen115Tokens] = useState({ access_token: '', refresh_token: '' })
  const [showOpen115Tokens, setShowOpen115Tokens] = useState(false)
  const [open115ImportLoading, setOpen115ImportLoading] = useState(false)
  const [open115Auth, setOpen115Auth] = useState(null)
  const [open115QRCodeURL, setOpen115QRCodeURL] = useState('')
  const [open115AuthLoading, setOpen115AuthLoading] = useState(false)
  const [cookie115Terminal, setCookie115Terminal] = useState('tv')
  const [cookie115Auth, setCookie115Auth] = useState(null)
  const [cookie115QRCodeURL, setCookie115QRCodeURL] = useState('')
  const [cookie115AuthLoading, setCookie115AuthLoading] = useState(false)
  const [pan123Credentials, setPan123Credentials] = useState(emptyPan123Credentials)
  const [showPan123ClientSecret, setShowPan123ClientSecret] = useState(false)
  const [pan123CredentialsSaving, setPan123CredentialsSaving] = useState(false)
  const [directoryPickerOpen, setDirectoryPickerOpen] = useState(false)
  const [directoryState, setDirectoryState] = useState(null)
  const [directoryLoading, setDirectoryLoading] = useState(false)
  const [directoryError, setDirectoryError] = useState('')
  const [newDirectoryName, setNewDirectoryName] = useState('')
  const [directoryFilter, setDirectoryFilter] = useState('')
  const open115Polling = useRef({ generation: 0, timer: null, controller: null })
  const cookie115Polling = useRef({ generation: 0, timer: null, controller: null })
  const providersState = useAsyncData(async (signal) => (await api.listProviders({ signal })).items || [], [])
  const secretsState = useAsyncData(async (signal) => {
    if (!selectedProviderId) return []
    return (await api.listProviderSecrets(selectedProviderId, { signal })).items || []
  }, [selectedProviderId])

  const isEditing = dialogMode === 'edit'

  function resetDialogState() {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    setProviderForm(emptyProvider)
    setDialogTab('settings')
    setSecretForm(emptySecret)
    setSelectedProviderId('')
    setMessage('')
    setShowSecretValue(false)
    setOpen115ClientId('')
    setOpen115Tokens({ access_token: '', refresh_token: '' })
    setShowOpen115Tokens(false)
    setOpen115ImportLoading(false)
    setOpen115Auth(null)
    setOpen115QRCodeURL('')
    setOpen115AuthLoading(false)
    setCookie115Terminal('tv')
    setCookie115Auth(null)
    setCookie115QRCodeURL('')
    setCookie115AuthLoading(false)
    setPan123Credentials(emptyPan123Credentials)
    setShowPan123ClientSecret(false)
    setPan123CredentialsSaving(false)
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

  useEffect(() => () => {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
  }, [])

  function openCreateDialog() {
    resetDialogState()
    setProviderActionError('')
    setDialogMode('create')
    setDialogOpen(true)
  }

  function openEditDialog(provider) {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    setProviderForm(withProviderDefaults(provider))
    setSecretForm(emptySecret)
    setSelectedProviderId(provider.id)
    setMessage('')
    setProviderActionError('')
    setShowSecretValue(false)
    setDialogTab('settings')
    setDialogMode('edit')
    setDialogOpen(true)
  }

  function closeDialog() {
    setDialogOpen(false)
    resetDialogState()
  }

  function handleProviderTypeChange(type) {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    setOpen115AuthLoading(false)
    setOpen115ImportLoading(false)
    setCookie115AuthLoading(false)
    setOpen115Auth(null)
    setOpen115QRCodeURL('')
    setOpen115Tokens({ access_token: '', refresh_token: '' })
    setCookie115Auth(null)
    setCookie115QRCodeURL('')
    setPan123Credentials(emptyPan123Credentials)
    setShowPan123ClientSecret(false)
    setProviderForm((current) => ({
      ...current,
      type,
      watch_enabled: supportsProviderWatch(type) ? current.watch_enabled : false,
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
    if (providerForm.type === '115cookie') {
      const minInterval = getCookie115RequestIntervalSeconds(providerForm.config, 'request_interval_min_seconds', defaultCookie115RequestIntervalMinSeconds)
      const maxInterval = getCookie115RequestIntervalSeconds(providerForm.config, 'request_interval_max_seconds', defaultCookie115RequestIntervalMaxSeconds)
      if (minInterval > maxInterval) {
        setMessage('最小请求间隔不能大于最大请求间隔。')
        return
      }
    }
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

  async function handleSavePan123Credentials(event) {
    event.preventDefault()
    setMessage('')
    setPan123CredentialsSaving(true)
    try {
      await api.saveProvider123PanCredentials(selectedProviderId, {
        client_id: pan123Credentials.client_id.trim(),
        client_secret: pan123Credentials.client_secret,
      })
      setPan123Credentials(emptyPan123Credentials)
      setShowPan123ClientSecret(false)
      await secretsState.refresh()
      const providers = await providersState.refresh()
      const refreshedProvider = providers?.find((provider) => provider.id === selectedProviderId)
      if (refreshedProvider) {
        setProviderForm(withProviderDefaults(refreshedProvider))
      }
      if (refreshedProvider?.status === 'healthy') {
        setMessage('123pan 开放平台凭据已保存并验证。')
      } else if (refreshedProvider?.status === 'error') {
        setMessage(`凭据已保存，但状态检查失败：${refreshedProvider.last_error || '请检查凭据和根路径。'}`)
      } else {
        setMessage('123pan 开放平台凭据已保存。')
      }
    } catch (error) {
      setMessage(error.message)
    } finally {
      setPan123CredentialsSaving(false)
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
    setProviderActionError('')
    try {
      await api.deleteProvider(providerId)
      await providersState.refresh()
      if (selectedProviderId === providerId) {
        closeDialog()
      }
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : String(error)
      setProviderActionError(errorMessage)
      if (selectedProviderId === providerId) {
        setMessage(errorMessage)
      }
    }
  }

  function scheduleCookie115AuthPoll(providerId, sessionId, generation, delay) {
    const polling = cookie115Polling.current
    if (polling.generation !== generation) {
      return
    }
    polling.timer = window.setTimeout(() => {
      polling.timer = null
      pollCookie115Auth(providerId, sessionId, generation)
    }, delay)
  }

  async function pollCookie115Auth(providerId, sessionId, generation) {
    const polling = cookie115Polling.current
    if (polling.generation !== generation) {
      return
    }

    const controller = new AbortController()
    polling.controller = controller
    try {
      const status = await api.getProvider115CookieAuthStatus(providerId, sessionId, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
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
      scheduleCookie115AuthPoll(providerId, sessionId, generation, 800)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      setCookie115Auth((current) => current ? { ...current, state: 'error', message: error.message } : null)
      setMessage(error.message)
      setCookie115AuthLoading(false)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
    }
  }

  async function handleStartCookie115Auth() {
    if (!selectedProviderId) {
      return
    }
    stopAuthPolling(cookie115Polling.current)
    stopAuthPolling(open115Polling.current)
    const polling = cookie115Polling.current
    const generation = polling.generation
    const providerId = selectedProviderId
    const controller = new AbortController()
    polling.controller = controller
    try {
      setMessage('')
      setCookie115AuthLoading(true)
      const session = await api.startProvider115CookieAuth(providerId, cookie115Terminal, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
      setCookie115Auth(session)
      setCookie115Terminal(session.terminal || cookie115Terminal)
      setMessage('请使用 115 App 扫码，然后在选择的终端类型上确认登录。')
      scheduleCookie115AuthPoll(providerId, session.session_id, generation, 300)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      setCookie115AuthLoading(false)
      setMessage(error.message)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
    }
  }

  function scheduleOpen115AuthPoll(providerId, sessionId, generation, delay) {
    const polling = open115Polling.current
    if (polling.generation !== generation) {
      return
    }
    polling.timer = window.setTimeout(() => {
      polling.timer = null
      pollOpen115Auth(providerId, sessionId, generation)
    }, delay)
  }

  async function pollOpen115Auth(providerId, sessionId, generation) {
    const polling = open115Polling.current
    if (polling.generation !== generation) {
      return
    }

    const controller = new AbortController()
    polling.controller = controller
    try {
      const status = await api.getProvider115OpenAuthStatus(providerId, sessionId, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
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
      scheduleOpen115AuthPoll(providerId, sessionId, generation, 800)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      setOpen115Auth((current) => current ? { ...current, state: 'error', message: error.message } : null)
      setMessage(error.message)
      setOpen115AuthLoading(false)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
    }
  }

  async function handleStart115OpenAuth() {
    if (!selectedProviderId) {
      return
    }
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    const polling = open115Polling.current
    const generation = polling.generation
    const providerId = selectedProviderId
    const controller = new AbortController()
    polling.controller = controller
    try {
      setMessage('')
      setOpen115AuthLoading(true)
      const session = await api.startProvider115OpenAuth(providerId, open115ClientId, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
      setOpen115Auth(session)
      setOpen115ClientId(session.client_id || open115ClientId)
      setMessage('请使用 115 App 扫码并确认授权。')
      scheduleOpen115AuthPoll(providerId, session.session_id, generation, 300)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      setOpen115AuthLoading(false)
      setMessage(error.message)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
    }
  }

  async function handleImport115OpenTokens(event) {
    event.preventDefault()
    if (!selectedProviderId) {
      return
    }
    try {
      setMessage('')
      setOpen115ImportLoading(true)
      await api.importProvider115OpenTokens(selectedProviderId, {
        client_id: open115ClientId.trim(),
        access_token: open115Tokens.access_token.trim(),
        refresh_token: open115Tokens.refresh_token.trim(),
      })
      setOpen115Tokens({ access_token: '', refresh_token: '' })
      setMessage('115 Open Token 已保存，将在下一次访问时自动校验和刷新。')
      await secretsState.refresh()
      await providersState.refresh()
    } catch (error) {
      setMessage(error.message)
    } finally {
      setOpen115ImportLoading(false)
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

  function handleScanRequestIntervalChange(value) {
    const parsed = Number.parseInt(value, 10)
    setProviderForm((current) => ({
      ...current,
      config: {
        ...(current.config || {}),
        scan_request_interval_ms: Number.isFinite(parsed) ? parsed : defaultScanRequestIntervalMs,
      },
    }))
  }

  function handleCookie115RequestIntervalChange(key, value) {
    const parsed = Number.parseInt(value, 10)
    setProviderForm((current) => ({
      ...current,
      config: {
        ...(current.config || {}),
        [key]: Number.isFinite(parsed) ? Math.max(1, parsed) : 1,
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
        {providerActionError ? <div className="banner banner-error">{providerActionError}</div> : null}
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
          <div className={`modal-card provider-modal-card${isEditing ? ' provider-modal-editing' : ''}`} role="dialog" aria-modal="true" aria-labelledby="provider-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header provider-modal-header">
              <div>
                <h2 id="provider-dialog-title">{isEditing ? '编辑数据源' : '添加数据源'}</h2>
                <p>{isEditing ? `${providerForm.type} · ${providerForm.id}` : '配置连接、扫描和附属文件规则。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeDialog}>关闭</button>
            </div>

            {isEditing ? (
              <div className="dialog-tabs" role="tablist" aria-label="数据源编辑区域">
                <button type="button" role="tab" aria-selected={dialogTab === 'settings'} className={dialogTab === 'settings' ? 'active' : ''} onClick={() => setDialogTab('settings')}>基础设置</button>
                <button type="button" role="tab" aria-selected={dialogTab === 'credentials'} className={dialogTab === 'credentials' ? 'active' : ''} onClick={() => setDialogTab('credentials')}>授权与密钥</button>
              </div>
            ) : null}

            <div className="provider-modal-body">
              {message ? <div className="banner provider-dialog-message">{message}</div> : null}

              {dialogTab === 'settings' ? (
                <form className="provider-settings-form" onSubmit={handleSubmitProvider}>
                  <section className="provider-form-section">
                    <div className="provider-section-heading">
                      <h3>基本信息</h3>
                      <span>数据源名称、类型与根路径</span>
                    </div>
                    <div className="provider-fields-grid">
                      <label className="form-field">
                        <span>名称</span>
                        <input value={providerForm.name} onChange={(e) => setProviderForm({ ...providerForm, name: e.target.value })} placeholder="例如：115 Open" required />
                      </label>
                      <label className="form-field">
                        <span>类型</span>
                        <select value={providerForm.type} onChange={(e) => handleProviderTypeChange(e.target.value)}>
                          <option value="local">local</option>
                          <option value="115cookie">115cookie</option>
                          <option value="115open">115open</option>
                          <option value="123pan">123pan</option>
                        </select>
                      </label>
                      <label className="form-field provider-root-field">
                        <span>根路径</span>
                        <div className="path-input-row">
                          <input value={providerForm.root_path} onChange={(e) => setProviderForm({ ...providerForm, root_path: e.target.value })} placeholder={isCloudProviderType(providerForm.type) ? '/ 或 /影视' : '根路径'} required />
                          {canBrowseProviderRoot ? <button type="button" className="ghost-button" onClick={openDirectoryPicker}>浏览</button> : null}
                        </div>
                      </label>
                    </div>
                  </section>

                  <section className="provider-form-section">
                    <div className="provider-section-heading">
                      <h3>运行设置</h3>
                      <span>控制数据源是否启用以及扫描请求节奏</span>
                    </div>
                    <div className="provider-runtime-row">
                      <label className="check-inline"><input type="checkbox" checked={providerForm.enabled} onChange={(e) => setProviderForm({ ...providerForm, enabled: e.target.checked })} /> 启用数据源</label>
                      <label className="check-inline"><input type="checkbox" checked={providerForm.watch_enabled} disabled={!supportsProviderWatch(providerForm.type)} onChange={(e) => setProviderForm({ ...providerForm, watch_enabled: e.target.checked })} /> 实时监听</label>
                    </div>
                    {providerForm.type === '115open' || providerForm.type === '115cookie' ? (
                      <div className="hint">使用 115 网盘完整路径，建议根路径保持为 <code>/</code>；当前不支持实时监听。</div>
                    ) : null}
                    {providerForm.type === '123pan' ? (
                      <div className="hint">使用 123 云盘完整路径，建议根路径保持为 <code>/</code>；当前不支持实时监听。</div>
                    ) : null}
                    {supportsScanRequestInterval(providerForm.type) ? (
                      <div className="provider-rate-setting">
                        <label className="form-field">
                          <span>扫描请求间隔</span>
                          <div className="input-with-suffix">
                            <input
                              type="number"
                              min="250"
                              max="10000"
                              value={getScanRequestIntervalMs(providerForm.config)}
                              onChange={(e) => handleScanRequestIntervalChange(e.target.value)}
                            />
                            <span>毫秒</span>
                          </div>
                        </label>
                        <div className="hint">仅限制扫描期间的 {providerForm.type === '123pan' ? '123pan' : '115 Open'} API 请求；默认 500ms，约每秒 2 次。</div>
                      </div>
                    ) : null}
                    {providerForm.type === '115cookie' ? (
                      <div className="provider-rate-setting">
                        <div className="provider-fields-grid provider-rate-range">
                          <label className="form-field">
                            <span>最小请求间隔</span>
                            <div className="input-with-suffix">
                              <input
                                type="number"
                                min="1"
                                value={getCookie115RequestIntervalSeconds(providerForm.config, 'request_interval_min_seconds', defaultCookie115RequestIntervalMinSeconds)}
                                onChange={(e) => handleCookie115RequestIntervalChange('request_interval_min_seconds', e.target.value)}
                              />
                              <span>秒</span>
                            </div>
                          </label>
                          <label className="form-field">
                            <span>最大请求间隔</span>
                            <div className="input-with-suffix">
                              <input
                                type="number"
                                min="1"
                                value={getCookie115RequestIntervalSeconds(providerForm.config, 'request_interval_max_seconds', defaultCookie115RequestIntervalMaxSeconds)}
                                onChange={(e) => handleCookie115RequestIntervalChange('request_interval_max_seconds', e.target.value)}
                              />
                              <span>秒</span>
                            </div>
                          </label>
                        </div>
                        <div className="hint">每次 115 Cookie API 请求会在该范围内随机等待；最低 1 秒。两个值相同时使用固定间隔。</div>
                      </div>
                    ) : null}
                  </section>

                  <section className="provider-form-section">
                    <div className="provider-section-heading">
                      <h3>附属文件</h3>
                      <span>选择扫描时生成或下载的文件类型</span>
                    </div>
                    <div className="download-config-grid provider-download-grid">
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.strm} onChange={(e) => handleDownloadToggle('strm', e.target.checked)} /> strm</label>
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.nfo} onChange={(e) => handleDownloadToggle('nfo', e.target.checked)} /> nfo</label>
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.images} onChange={(e) => handleDownloadToggle('images', e.target.checked)} /> images</label>
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.subtitles} onChange={(e) => handleDownloadToggle('subtitles', e.target.checked)} /> subtitles</label>
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.bif} onChange={(e) => handleDownloadToggle('bif', e.target.checked)} /> bif</label>
                      <label className="check-inline"><input type="checkbox" checked={downloadConfig.mediainfo} onChange={(e) => handleDownloadToggle('mediainfo', e.target.checked)} /> mediainfo.json</label>
                    </div>
                  </section>

                  <section className="provider-form-section">
                    <div className="provider-section-heading">
                      <h3>Webhook 路径</h3>
                      <span>可选；每行填写一个需要去掉的路径前缀</span>
                    </div>
                    <textarea value={webhookPrefixes.join('\n')} onChange={(e) => handleWebhookPrefixesChange(e.target.value)} rows={3} placeholder={'例如：\n/115open'} />
                    <div className="hint">留空时直接使用请求中的网盘路径。仅当 CloudDrive2 路径带有额外挂载前缀时填写，例如 <code>/115open</code>；匹配后会去掉该前缀，再匹配数据源的启用映射。</div>
                  </section>

                  <div className="provider-dialog-actions">
                    <button type="submit">{isEditing ? '保存数据源' : '创建数据源'}</button>
                    {isEditing ? <button type="button" className="danger" onClick={() => handleDeleteProvider(providerForm.id)}>删除数据源</button> : null}
                  </div>
                </form>
              ) : (
                <section className="provider-credentials-view">
                  <div className="section-heading">
                    <div>
                      <h3>{providerForm.type === '115open' ? '115 Open 授权' : providerForm.type === '115cookie' ? '115 Cookie 登录' : providerForm.type === '123pan' ? '123pan 开放平台凭据' : '数据源密钥'}</h3>
                      <p>管理登录凭据和数据源访问密钥。</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={secretsState.refresh}>刷新密钥</button>
                  </div>

                  {selectedProviderId && providerForm.type === '115open' ? (
                    <div className="provider-auth-grid">
                      <section className="provider-auth-panel">
                        <div className="provider-section-heading">
                          <h3>扫码授权</h3>
                          <span>使用自己的 Client ID 完成 PKCE 授权</span>
                        </div>
                        <label className="form-field">
                          <span>Client ID（可选）</span>
                          <input value={open115ClientId} onChange={(e) => setOpen115ClientId(e.target.value)} placeholder="自己的 115 Open AppID" />
                        </label>
                        <button type="button" onClick={handleStart115OpenAuth} disabled={open115AuthLoading}>{open115AuthLoading ? '授权中...' : '开始扫码授权'}</button>
                        <div className="hint">留空时使用已保存的 <code>client_id</code>。PKCE 授权不需要 AppSecret。</div>
                        {open115Auth ? (
                          <div className="provider-auth-result">
                            <div className="hint">状态：{open115Auth.state}{open115Auth.message ? ` · ${open115Auth.message}` : ''}</div>
                            {open115QRCodeURL ? <img src={open115QRCodeURL} alt="115open auth qr" className="provider-auth-qr" /> : null}
                            {open115Auth.qr_code ? <div className="hint">二维码内容：<code>{open115Auth.qr_code}</code></div> : null}
                            {open115Auth.access_token ? <textarea readOnly value={open115Auth.access_token} rows={3} /> : null}
                            {open115Auth.refresh_token ? <textarea readOnly value={open115Auth.refresh_token} rows={3} /> : null}
                          </div>
                        ) : null}
                      </section>

                      <section className="provider-auth-panel">
                        <div className="provider-section-heading">
                          <h3>直接导入 Token</h3>
                          <span>使用 OpenList 或其他 Client ID 获取的凭据</span>
                        </div>
                        <form className="form-grid" onSubmit={handleImport115OpenTokens}>
                          <label className="form-field">
                            <span>Access Token</span>
                            <input type={showOpen115Tokens ? 'text' : 'password'} value={open115Tokens.access_token} onChange={(event) => setOpen115Tokens((current) => ({ ...current, access_token: event.target.value }))} autoComplete="off" />
                          </label>
                          <label className="form-field">
                            <span>Refresh Token</span>
                            <input type={showOpen115Tokens ? 'text' : 'password'} value={open115Tokens.refresh_token} onChange={(event) => setOpen115Tokens((current) => ({ ...current, refresh_token: event.target.value }))} autoComplete="off" />
                          </label>
                          <div className="button-row">
                            <button type="submit" disabled={open115ImportLoading || (!open115Tokens.access_token.trim() && !open115Tokens.refresh_token.trim())}>{open115ImportLoading ? '保存中...' : '导入 Token'}</button>
                            <button type="button" className="ghost-button" onClick={() => setShowOpen115Tokens((current) => !current)}>{showOpen115Tokens ? '隐藏 Token' : '显示 Token'}</button>
                          </div>
                        </form>
                        <div className="hint">可从 <a href="https://api.oplist.org" target="_blank" rel="noreferrer">api.oplist.org</a> 等服务获取。建议同时填写两种 Token；后续刷新不需要 Client ID 或 AppKey。</div>
                      </section>
                    </div>
                  ) : null}

                  {selectedProviderId && providerForm.type === '115cookie' ? (
                    <section className="provider-auth-panel provider-cookie-panel">
                      <label className="form-field">
                        <span>登录终端</span>
                        <select value={cookie115Terminal} onChange={(e) => setCookie115Terminal(e.target.value)}>
                          {['tv', 'alipaymini', 'wechatmini', 'qandroid', 'web', 'android', 'ios'].map((terminal) => (
                            <option key={terminal} value={terminal}>{terminal}</option>
                          ))}
                        </select>
                      </label>
                      <button type="button" onClick={handleStartCookie115Auth} disabled={cookie115AuthLoading}>{cookie115AuthLoading ? '登录中...' : '开始扫码登录'}</button>
                      <div className="hint">推荐 <code>tv</code>、<code>alipaymini</code>、<code>wechatmini</code>、<code>qandroid</code>。相同终端类型可能挤掉已有会话。</div>
                      {cookie115Auth ? (
                        <div className="provider-auth-result">
                          <div className="hint">状态：{cookie115Auth.state}{cookie115Auth.message ? ` · ${cookie115Auth.message}` : ''}</div>
                          {cookie115QRCodeURL ? <img src={cookie115QRCodeURL} alt="115 cookie login qr" className="provider-auth-qr" /> : null}
                          {cookie115Auth.qr_code ? <div className="hint">二维码内容：<code>{cookie115Auth.qr_code}</code></div> : null}
                          {cookie115Auth.cookie ? <textarea readOnly value={cookie115Auth.cookie} rows={3} /> : null}
                        </div>
                      ) : null}
                    </section>
                  ) : null}

                  {selectedProviderId && providerForm.type === '123pan' ? (
                    <section className="provider-auth-panel provider-cookie-panel">
                      <div className="provider-section-heading">
                        <h3>开放平台应用</h3>
                        <span>服务会自动申请并续期 Access Token</span>
                      </div>
                      <form className="form-grid" onSubmit={handleSavePan123Credentials}>
                        <label className="form-field">
                          <span>Client ID</span>
                          <input
                            value={pan123Credentials.client_id}
                            onChange={(event) => setPan123Credentials((current) => ({ ...current, client_id: event.target.value }))}
                            autoComplete="off"
                            required
                          />
                        </label>
                        <label className="form-field">
                          <span>Client Secret</span>
                          <div className="secret-input-row">
                            <input
                              type={showPan123ClientSecret ? 'text' : 'password'}
                              value={pan123Credentials.client_secret}
                              onChange={(event) => setPan123Credentials((current) => ({ ...current, client_secret: event.target.value }))}
                              autoComplete="new-password"
                              required
                            />
                            <button type="button" className="ghost-button" onClick={() => setShowPan123ClientSecret((current) => !current)}>{showPan123ClientSecret ? '隐藏' : '显示'}</button>
                          </div>
                        </label>
                        <div className="button-row">
                          <button type="submit" disabled={pan123CredentialsSaving}>{pan123CredentialsSaving ? '保存中...' : '保存凭据'}</button>
                        </div>
                      </form>
                      <div className="hint">在 <a href="https://www.123pan.com/developer" target="_blank" rel="noreferrer">123 云盘开放平台</a>创建应用后填写。Access Token 由 NyaMedia 管理，无需手动录入。</div>
                    </section>
                  ) : null}

                  <details className="advanced-secrets" open={providerForm.type === 'local'}>
                    <summary>高级：手动维护密钥</summary>
                    <div className="advanced-secrets-body">
                      {providerForm.type !== '123pan' ? (
                        <form className="provider-secret-form" onSubmit={handleSaveSecret}>
                          <label className="form-field">
                            <span>密钥类型</span>
                            <input value={secretForm.type} onChange={(e) => { setSecretForm({ ...secretForm, type: e.target.value }); setMessage('') }} placeholder="例如：client_id" required />
                          </label>
                          <label className="form-field">
                            <span>密钥值</span>
                            <div className="secret-input-row">
                              <input type={showSecretValue ? 'text' : 'password'} value={secretForm.value} onChange={(e) => { setSecretForm({ ...secretForm, value: e.target.value }); setMessage('') }} required />
                              <button type="button" className="ghost-button" onClick={() => setShowSecretValue((current) => !current)}>{showSecretValue ? '隐藏' : '显示'}</button>
                            </div>
                          </label>
                          <div className="button-row provider-secret-submit">
                            <button type="submit">保存密钥</button>
                          </div>
                        </form>
                      ) : null}
                      {providerForm.type === '115open' ? <div className="hint">通常无需手动维护；专用表单会同时处理 <code>client_id</code>、<code>access_token</code> 和 <code>refresh_token</code>。</div> : null}
                      {providerForm.type === '115cookie' ? <div className="hint">扫码登录会自动记录 <code>cookie</code> 和 <code>platform</code>，也可手动添加 <code>user_agent</code>。</div> : null}
                      {providerForm.type === '123pan' ? <div className="hint">通常无需手动维护；专用表单保存 <code>client_id</code> 和 <code>client_secret</code>，Token 由服务自动维护。</div> : null}
                      <StatusBanner error={secretsState.error} loading={secretsState.loading}>
                        <div className="table-wrap">
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
                                    {providerForm.type === '123pan' ? (
                                      <span className="hint">由专用表单管理</span>
                                    ) : (
                                      <div className="button-row">
                                        <button type="button" className="ghost-button" onClick={() => { setSecretForm({ type: secret.secret_type, value: '' }); setMessage('请输入新值来更新该密钥。') }}>编辑</button>
                                        <button type="button" className="danger" onClick={() => handleDeleteSecret(secret.secret_type)}>删除</button>
                                      </div>
                                    )}
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
                    </div>
                  </details>
                </section>
              )}
            </div>

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
