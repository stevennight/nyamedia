import { useCallback, useEffect, useRef, useState } from 'react'
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
const providerStatusConcurrency = 4
const emptyProvider = { id: '', type: 'local', name: '', root_path: '', enabled: true, watch_enabled: true, config: { downloads: { ...defaultDownloads }, webhook: { path_prefixes: [] } } }
const emptySecret = { type: '', value: '' }
const emptyPan123Credentials = { client_id: '', client_secret: '' }
const emptyBaiduOpenCredentials = { client_id: '', client_secret: '', client_secret_configured: false }
const emptyBaiduOpenBrokerConfig = { base_url: '', client_id: '', token: '', token_configured: false, configured: false }
const emptyBaiduOpenTokens = { access_token: '', refresh_token: '', access_token_configured: false, refresh_token_configured: false }
const open115AuthMethods = [
  ['qr', '扫码授权'],
  ['token_import', '导入 Token'],
]
const baiduOpenAuthMethods = [
  ['official', '官方直连'],
  ['broker_relay', 'Broker Relay'],
  ['broker_token_exchange', 'Broker 手动'],
]

function joinPublicURL(baseURL, path) {
  return baseURL ? `${baseURL.replace(/\/+$/, '')}/${path.replace(/^\/+/, '')}` : ''
}

function openAuthorizationWindow(authorizationURL) {
  const authWindow = window.open(authorizationURL, '_blank')
  if (!authWindow) {
    return false
  }
  authWindow.opener = null
  return true
}

function isCloudProviderType(type) {
  return type === '115open' || type === '115cookie' || type === '123pan' || type === 'baiduopen'
}

function supportsProviderWatch(type) {
  return type === 'local'
}

function supportsScanRequestInterval(type) {
  return type === '115open' || type === '123pan' || type === 'baiduopen'
}

function stopAuthPolling(polling) {
  polling.generation += 1
  if (polling.timer !== null) {
    window.clearTimeout(polling.timer)
  }
  polling.controller?.abort()
  polling.timer = null
  polling.controller = null
  polling.backoffDelay = 0
}

function AuthMethodSelector({ label, value, options, onChange, disabled = false }) {
  return (
    <div
      className="auth-mode-segments"
      role="tablist"
      aria-label={label}
      style={{ '--auth-method-count': options.length }}
    >
      {options.map(([mode, text]) => (
        <button
          key={mode}
          type="button"
          role="tab"
          aria-selected={value === mode}
          className={value === mode ? 'active' : ''}
          onClick={() => onChange(mode)}
          disabled={disabled}
        >
          {text}
        </button>
      ))}
    </div>
  )
}

function useProviderList() {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [statusCheckingIds, setStatusCheckingIds] = useState(() => new Set())
  const mounted = useRef(false)
  const hasData = useRef(false)
  const listRequest = useRef({ sequence: 0, controller: null })
  const statusRequest = useRef({ generation: 0, controller: null })

  const stopStatusChecks = useCallback(() => {
    statusRequest.current.generation += 1
    statusRequest.current.controller?.abort()
    statusRequest.current.controller = null
    if (mounted.current) {
      setStatusCheckingIds(new Set())
    }
  }, [])

  const checkStatuses = useCallback((providers) => {
    statusRequest.current.generation += 1
    statusRequest.current.controller?.abort()

    if (!providers.length) {
      statusRequest.current.controller = null
      setStatusCheckingIds(new Set())
      return
    }

    const generation = statusRequest.current.generation
    const controller = new AbortController()
    statusRequest.current.controller = controller
    setStatusCheckingIds(new Set(providers.map((provider) => provider.id)))

    let cursor = 0
    async function worker() {
      while (cursor < providers.length && !controller.signal.aborted) {
        const provider = providers[cursor]
        cursor += 1
        let checkedProvider
        try {
          checkedProvider = await api.getProvider(provider.id, { signal: controller.signal })
        } catch (requestError) {
          if (controller.signal.aborted || requestError?.name === 'AbortError') {
            return
          }
          checkedProvider = {
            ...provider,
            status: 'error',
            last_error: requestError instanceof Error ? requestError.message : String(requestError),
            last_check_at: new Date().toISOString(),
          }
        }

        if (mounted.current && statusRequest.current.generation === generation && !controller.signal.aborted) {
          setData((current) => current?.map((item) => (item.id === checkedProvider.id ? checkedProvider : item)) || current)
          setStatusCheckingIds((current) => {
            const next = new Set(current)
            next.delete(provider.id)
            return next
          })
        }
      }
    }

    const workerCount = Math.min(providerStatusConcurrency, providers.length)
    void Promise.all(Array.from({ length: workerCount }, () => worker())).finally(() => {
      if (statusRequest.current.generation === generation) {
        statusRequest.current.controller = null
      }
    })
  }, [])

  const refresh = useCallback(async () => {
    const sequence = listRequest.current.sequence + 1
    listRequest.current.sequence = sequence
    listRequest.current.controller?.abort()
    stopStatusChecks()

    const controller = new AbortController()
    listRequest.current.controller = controller
    if (mounted.current) {
      if (!hasData.current) {
        setLoading(true)
      }
      setError('')
    }

    try {
      const providers = (await api.listProviders({ checkStatus: false, signal: controller.signal })).items || []
      if (mounted.current && listRequest.current.sequence === sequence && !controller.signal.aborted) {
        hasData.current = true
        setData(providers)
        checkStatuses(providers)
      }
      return providers
    } catch (requestError) {
      if (controller.signal.aborted || requestError?.name === 'AbortError') {
        return undefined
      }
      if (mounted.current && listRequest.current.sequence === sequence) {
        setError(requestError instanceof Error ? requestError.message : String(requestError))
      }
      return undefined
    } finally {
      if (listRequest.current.sequence === sequence) {
        listRequest.current.controller = null
        if (mounted.current) {
          setLoading(false)
        }
      }
    }
  }, [checkStatuses, stopStatusChecks])

  useEffect(() => {
    mounted.current = true
    refresh()
    return () => {
      mounted.current = false
      listRequest.current.controller?.abort()
      statusRequest.current.controller?.abort()
    }
  }, [refresh])

  return { data, error, loading, refresh, setData, statusCheckingIds, stopStatusChecks }
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
  const { systemTimeZone, publicBaseURL } = useOutletContext() || {}
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
  const [open115AuthMode, setOpen115AuthMode] = useState('qr')
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
  const [baiduOpenCredentials, setBaiduOpenCredentials] = useState(emptyBaiduOpenCredentials)
  const [showBaiduOpenClientSecret, setShowBaiduOpenClientSecret] = useState(false)
  const [baiduOpenCredentialsSaving, setBaiduOpenCredentialsSaving] = useState(false)
  const [baiduOpenBrokerConfig, setBaiduOpenBrokerConfig] = useState(emptyBaiduOpenBrokerConfig)
  const [showBaiduOpenBrokerToken, setShowBaiduOpenBrokerToken] = useState(false)
  const [baiduOpenBrokerSaving, setBaiduOpenBrokerSaving] = useState(false)
  const [baiduOpenAuthMode, setBaiduOpenAuthMode] = useState('official')
  const [baiduOpenAuthModeSaving, setBaiduOpenAuthModeSaving] = useState(false)
  const [baiduOpenTokens, setBaiduOpenTokens] = useState(emptyBaiduOpenTokens)
  const [showBaiduOpenTokens, setShowBaiduOpenTokens] = useState(false)
  const [baiduOpenTokenImportLoading, setBaiduOpenTokenImportLoading] = useState(false)
  const [baiduOpenAuth, setBaiduOpenAuth] = useState(null)
  const [baiduOpenAuthLoading, setBaiduOpenAuthLoading] = useState(false)
  const [directoryPickerOpen, setDirectoryPickerOpen] = useState(false)
  const [directoryState, setDirectoryState] = useState(null)
  const [directoryLoading, setDirectoryLoading] = useState(false)
  const [directoryError, setDirectoryError] = useState('')
  const [newDirectoryName, setNewDirectoryName] = useState('')
  const [directoryFilter, setDirectoryFilter] = useState('')
  const open115Polling = useRef({ generation: 0, timer: null, controller: null })
  const cookie115Polling = useRef({ generation: 0, timer: null, controller: null })
  const baiduOpenPolling = useRef({ generation: 0, timer: null, controller: null })
  const providersState = useProviderList()
  const secretsState = useAsyncData(async (signal) => {
    if (!selectedProviderId) return []
    return (await api.listProviderSecrets(selectedProviderId, { signal })).items || []
  }, [selectedProviderId])

  const isEditing = dialogMode === 'edit'
  const baiduOpenCallbackURI = selectedProviderId
    ? joinPublicURL(publicBaseURL, `/api/v1/providers/${encodeURIComponent(selectedProviderId)}/auth/baiduopen/callback`)
    : ''
  const baiduOpenBrokerCallbackURI = joinPublicURL(baiduOpenBrokerConfig.base_url, '/v1/callbacks/baidu')

  function resetDialogState() {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    stopAuthPolling(baiduOpenPolling.current)
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
    setOpen115AuthMode('qr')
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
    setBaiduOpenCredentials(emptyBaiduOpenCredentials)
    setShowBaiduOpenClientSecret(false)
    setBaiduOpenCredentialsSaving(false)
    setBaiduOpenBrokerConfig(emptyBaiduOpenBrokerConfig)
    setShowBaiduOpenBrokerToken(false)
    setBaiduOpenBrokerSaving(false)
    setBaiduOpenAuthMode('official')
    setBaiduOpenAuthModeSaving(false)
    setBaiduOpenTokens(emptyBaiduOpenTokens)
    setShowBaiduOpenTokens(false)
    setBaiduOpenTokenImportLoading(false)
    setBaiduOpenAuth(null)
    setBaiduOpenAuthLoading(false)
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

  useEffect(() => {
    if (!selectedProviderId || providerForm.type !== 'baiduopen') {
      setBaiduOpenCredentials(emptyBaiduOpenCredentials)
      setBaiduOpenBrokerConfig(emptyBaiduOpenBrokerConfig)
      setBaiduOpenTokens(emptyBaiduOpenTokens)
      setBaiduOpenAuthMode('official')
      return undefined
    }
    const controller = new AbortController()
    Promise.all([
      api.getProviderBaiduOpenAuthConfig(selectedProviderId, { signal: controller.signal }),
      api.getProviderBaiduOpenBrokerConfig(selectedProviderId, { signal: controller.signal }),
    ])
      .then(([authConfig, brokerConfig]) => {
        if (!controller.signal.aborted) {
          setBaiduOpenCredentials({
            ...emptyBaiduOpenCredentials,
            client_id: authConfig.client_id || '',
            client_secret_configured: Boolean(authConfig.client_secret_configured),
          })
          setBaiduOpenTokens({
            ...emptyBaiduOpenTokens,
            access_token_configured: Boolean(authConfig.access_token_configured),
            refresh_token_configured: Boolean(authConfig.refresh_token_configured),
          })
          setBaiduOpenAuthMode(authConfig.auth_mode || 'official')
          setBaiduOpenBrokerConfig({ ...emptyBaiduOpenBrokerConfig, ...brokerConfig, token: '' })
          setProviderForm((current) => ({
            ...current,
            config: {
              ...(current.config || {}),
              baiduopen_auth_mode: authConfig.auth_mode || 'official',
            },
          }))
        }
      })
      .catch((error) => {
        if (!controller.signal.aborted && error?.name !== 'AbortError') {
          setMessage(error.message)
        }
      })
    return () => controller.abort()
  }, [selectedProviderId, providerForm.type])

  useEffect(() => () => {
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    stopAuthPolling(baiduOpenPolling.current)
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
    stopAuthPolling(baiduOpenPolling.current)
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
    stopAuthPolling(baiduOpenPolling.current)
    setOpen115AuthLoading(false)
    setOpen115ImportLoading(false)
    setCookie115AuthLoading(false)
    setBaiduOpenAuthLoading(false)
    setOpen115Auth(null)
    setOpen115QRCodeURL('')
    setOpen115Tokens({ access_token: '', refresh_token: '' })
    setOpen115AuthMode('qr')
    setCookie115Auth(null)
    setCookie115QRCodeURL('')
    setPan123Credentials(emptyPan123Credentials)
    setShowPan123ClientSecret(false)
    setBaiduOpenCredentials(emptyBaiduOpenCredentials)
    setShowBaiduOpenClientSecret(false)
    setBaiduOpenBrokerConfig(emptyBaiduOpenBrokerConfig)
    setShowBaiduOpenBrokerToken(false)
    setBaiduOpenAuthMode('official')
    setBaiduOpenAuthModeSaving(false)
    setBaiduOpenTokens(emptyBaiduOpenTokens)
    setShowBaiduOpenTokens(false)
    setBaiduOpenAuth(null)
    setBaiduOpenCredentialsSaving(false)
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
    providersState.stopStatusChecks()
    try {
      await api.saveProvider123PanCredentials(selectedProviderId, {
        client_id: pan123Credentials.client_id.trim(),
        client_secret: pan123Credentials.client_secret,
      })
      setPan123Credentials(emptyPan123Credentials)
      setShowPan123ClientSecret(false)
      await secretsState.refresh()
      const refreshedProvider = await api.getProvider(selectedProviderId)
      if (refreshedProvider) {
        setProviderForm(withProviderDefaults(refreshedProvider))
        providersState.setData((current) => current?.map((provider) => (provider.id === refreshedProvider.id ? refreshedProvider : provider)) || current)
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

  async function handleSaveBaiduOpenCredentials(event) {
    event.preventDefault()
    setMessage('')
    setBaiduOpenCredentialsSaving(true)
    try {
      const saved = await api.saveProviderBaiduOpenCredentials(selectedProviderId, {
        client_id: baiduOpenCredentials.client_id.trim(),
        client_secret: baiduOpenCredentials.client_secret,
      })
      setBaiduOpenCredentials({
        ...emptyBaiduOpenCredentials,
        client_id: saved.client_id || '',
        client_secret_configured: Boolean(saved.client_secret_configured),
      })
      setBaiduOpenTokens((current) => ({
        ...current,
        access_token_configured: Boolean(saved.access_token_configured),
        refresh_token_configured: Boolean(saved.refresh_token_configured),
      }))
      setShowBaiduOpenClientSecret(false)
      setBaiduOpenAuth(null)
      await secretsState.refresh()
      await providersState.refresh()
      setMessage('百度开放平台应用凭据已保存。下一步请发起百度账号授权。')
    } catch (error) {
      setMessage(error.message)
    } finally {
      setBaiduOpenCredentialsSaving(false)
    }
  }

  async function handleSaveBaiduOpenBrokerConfig(event) {
    event.preventDefault()
    setMessage('')
    setBaiduOpenBrokerSaving(true)
    try {
      const saved = await api.saveProviderBaiduOpenBrokerConfig(selectedProviderId, {
        base_url: baiduOpenBrokerConfig.base_url.trim(),
        client_id: baiduOpenBrokerConfig.client_id.trim(),
        token: baiduOpenBrokerConfig.token,
      })
      setBaiduOpenBrokerConfig({ ...emptyBaiduOpenBrokerConfig, ...saved, token: '' })
      setShowBaiduOpenBrokerToken(false)
      await secretsState.refresh()
      setMessage('OAuth Broker 配置已保存到当前百度数据源。')
    } catch (error) {
      setMessage(error.message)
    } finally {
      setBaiduOpenBrokerSaving(false)
    }
  }

  async function handleImportBaiduOpenTokens(event) {
    event.preventDefault()
    if (!selectedProviderId) {
      return
    }
    setMessage('')
    setBaiduOpenTokenImportLoading(true)
    try {
      const saved = await api.importProviderBaiduOpenTokens(selectedProviderId, {
        access_token: baiduOpenTokens.access_token.trim(),
        refresh_token: baiduOpenTokens.refresh_token.trim(),
      })
      setBaiduOpenTokens({
        ...emptyBaiduOpenTokens,
        access_token_configured: Boolean(saved.access_token_configured),
        refresh_token_configured: Boolean(saved.refresh_token_configured),
      })
      setShowBaiduOpenTokens(false)
      await secretsState.refresh()
      await providersState.refresh()
      setMessage('百度 Token 已校验并保存，将使用同一套应用凭据自动刷新。')
    } catch (error) {
      setMessage(error.message)
    } finally {
      setBaiduOpenTokenImportLoading(false)
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

  function handle115OpenAuthModeChange(mode) {
    stopAuthPolling(open115Polling.current)
    setOpen115AuthLoading(false)
    setOpen115Auth(null)
    setOpen115QRCodeURL('')
    setOpen115AuthMode(mode)
  }

  async function handleBaiduOpenAuthModeChange(mode) {
    if (mode === baiduOpenAuthMode || baiduOpenAuthModeSaving || !selectedProviderId) {
      return
    }
    const previousMode = baiduOpenAuthMode
    stopAuthPolling(baiduOpenPolling.current)
    setBaiduOpenAuthLoading(false)
    setBaiduOpenAuth(null)
    setBaiduOpenAuthMode(mode)
    setBaiduOpenAuthModeSaving(true)
    setMessage('')
    try {
      const saved = await api.saveProviderBaiduOpenAuthMode(selectedProviderId, mode)
      const savedMode = saved.auth_mode || mode
      setBaiduOpenAuthMode(savedMode)
      setProviderForm((current) => ({
        ...current,
        config: {
          ...(current.config || {}),
          baiduopen_auth_mode: savedMode,
        },
      }))
    } catch (error) {
      setBaiduOpenAuthMode(previousMode)
      setMessage(error.message)
    } finally {
      setBaiduOpenAuthModeSaving(false)
    }
  }

  function scheduleBaiduOpenAuthPoll(providerId, sessionId, generation, delay) {
    const polling = baiduOpenPolling.current
    if (polling.generation !== generation) {
      return
    }
    polling.timer = window.setTimeout(() => {
      polling.timer = null
      pollBaiduOpenAuth(providerId, sessionId, generation)
    }, delay)
  }

  async function pollBaiduOpenAuth(providerId, sessionId, generation) {
    const polling = baiduOpenPolling.current
    if (polling.generation !== generation) {
      return
    }

    const controller = new AbortController()
    polling.controller = controller
    try {
      const status = await api.getProviderBaiduOpenAuthStatus(providerId, sessionId, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
      setBaiduOpenAuth(status)
      polling.backoffDelay = 0
      if (status.state === 'authorized') {
        setMessage('百度网盘授权成功，Token 已保存并会自动刷新。')
        setBaiduOpenTokens((current) => ({
          ...current,
          access_token_configured: true,
          refresh_token_configured: true,
        }))
        secretsState.refresh()
        providersState.refresh()
        setBaiduOpenAuthLoading(false)
        return
      }
      if (status.state === 'completed' && status.mode === 'broker_token_exchange') {
        setMessage('Broker 已显示 Token，请从结果页复制后粘贴到下方导入。')
        setBaiduOpenAuthLoading(false)
        return
      }
      if (['expired', 'cancelled', 'error', 'failed'].includes(status.state)) {
        setMessage(status.message || '百度网盘授权已停止。')
        setBaiduOpenAuthLoading(false)
        return
      }
      scheduleBaiduOpenAuthPoll(providerId, sessionId, generation, status.mode === 'broker_token_exchange' ? 2000 : 1000)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      if (error?.status === 429) {
        polling.backoffDelay = Math.min(polling.backoffDelay ? polling.backoffDelay * 2 : 4000, 30000)
        setMessage(`OAuth Broker 请求受限，将在 ${Math.ceil(polling.backoffDelay / 1000)} 秒后重试。`)
        scheduleBaiduOpenAuthPoll(providerId, sessionId, generation, polling.backoffDelay)
        return
      }
      setBaiduOpenAuth((current) => current ? { ...current, state: 'error', message: error.message } : null)
      setMessage(error.message)
      setBaiduOpenAuthLoading(false)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
    }
  }

  async function handleStartBaiduOpenAuth(mode = baiduOpenAuthMode) {
    if (!selectedProviderId) {
      return
    }
    stopAuthPolling(baiduOpenPolling.current)
    stopAuthPolling(open115Polling.current)
    stopAuthPolling(cookie115Polling.current)
    const polling = baiduOpenPolling.current
    const generation = polling.generation
    const providerId = selectedProviderId
    const controller = new AbortController()
    polling.controller = controller
    try {
      setMessage('')
      setBaiduOpenAuthLoading(true)
      setBaiduOpenAuthMode(mode)
      const session = await api.startProviderBaiduOpenAuth(providerId, mode, { signal: controller.signal })
      if (polling.generation !== generation || controller.signal.aborted) {
        return
      }
      const authorizationWindowOpened = openAuthorizationWindow(session.authorization_url)
      if (mode === 'broker_token_exchange') {
        setBaiduOpenAuth(null)
        setBaiduOpenAuthLoading(false)
        if (!authorizationWindowOpened) {
          window.location.assign(session.authorization_url)
          return
        }
        setMessage('Broker 已在新标签打开。取得 Token 后回到此页导入。')
        return
      }
      setBaiduOpenAuth(session)
      setMessage(authorizationWindowOpened
        ? '请在百度授权页确认网盘访问权限。'
        : '浏览器拦截了授权弹窗，请使用下方链接打开授权页。')
      scheduleBaiduOpenAuthPoll(providerId, session.session_id, generation, 1000)
    } catch (error) {
      if (polling.generation !== generation || controller.signal.aborted || error?.name === 'AbortError') {
        return
      }
      setBaiduOpenAuthLoading(false)
      setMessage(error.message)
    } finally {
      if (polling.controller === controller) {
        polling.controller = null
      }
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
                    <td>{providersState.statusCheckingIds.has(provider.id) ? '检测中...' : formatProviderStatus(provider.status)}</td>
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
                          <option value="baiduopen">baiduopen</option>
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
                    {providerForm.type === 'baiduopen' ? (
                      <div className="hint">使用百度网盘完整路径，建议根路径保持为 <code>/</code>；当前不支持实时监听。</div>
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
                        <div className="hint">仅限制扫描期间的 {providerForm.type === '123pan' ? '123pan' : providerForm.type === 'baiduopen' ? '百度网盘 Open' : '115 Open'} API 请求；默认 500ms，约每秒 2 次；频控响应会有限退避重试。</div>
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
                      <h3>{providerForm.type === '115open' ? '115 Open 授权' : providerForm.type === '115cookie' ? '115 Cookie 登录' : providerForm.type === 'baiduopen' ? '百度网盘开放平台' : providerForm.type === '123pan' ? '123pan 开放平台凭据' : '数据源密钥'}</h3>
                      <p>管理登录凭据和数据源访问密钥。</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={secretsState.refresh}>刷新密钥</button>
                  </div>

                  {selectedProviderId && providerForm.type === '115open' ? (
                    <section className="provider-auth-workflow">
                      <div className="provider-section-heading">
                        <h3>接入方式</h3>
                        <span>选择一种方式配置当前数据源</span>
                      </div>
                      <AuthMethodSelector
                        label="115 Open 接入方式"
                        value={open115AuthMode}
                        options={open115AuthMethods}
                        onChange={handle115OpenAuthModeChange}
                      />

                      {open115AuthMode === 'qr' ? (
                        <div className="provider-auth-method" role="tabpanel">
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
                        </div>
                      ) : null}

                      {open115AuthMode === 'token_import' ? (
                        <div className="provider-auth-method" role="tabpanel">
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
                        </div>
                      ) : null}
                    </section>
                  ) : null}

                  {selectedProviderId && providerForm.type === 'baiduopen' ? (
                    <section className="provider-auth-workflow baidu-auth-stack">
                      <div className="provider-section-heading">
                        <h3>接入方式</h3>
                        <span>{baiduOpenAuthModeSaving ? '正在保存接入方式...' : '选择后自动保存到当前数据源'}</span>
                      </div>
                      <AuthMethodSelector
                        label="百度授权方式"
                        value={baiduOpenAuthMode}
                        options={baiduOpenAuthMethods}
                        onChange={handleBaiduOpenAuthModeChange}
                        disabled={baiduOpenAuthModeSaving}
                      />

                      <div className="provider-auth-method" role="tabpanel">
                        <div className={`provider-auth-grid${baiduOpenAuthMode === 'official' ? ' single-column' : ''}`}>
                        <section className="provider-auth-panel">
                          <div className="provider-section-heading">
                            <h3>百度开放平台应用</h3>
                            <span>{baiduOpenAuthMode === 'broker_token_exchange' ? '必须与 Broker 页面填写的应用凭据一致' : '用于发起授权和后续 Token 刷新'}</span>
                          </div>
                          <form className="form-grid" onSubmit={handleSaveBaiduOpenCredentials}>
                            <label className="form-field">
                              <span>Client ID / API Key</span>
                              <input
                                value={baiduOpenCredentials.client_id}
                                onChange={(event) => setBaiduOpenCredentials((current) => ({ ...current, client_id: event.target.value }))}
                                autoComplete="off"
                                required
                              />
                            </label>
                            <label className="form-field">
                              <span>Client Secret / Secret Key</span>
                              <div className="secret-input-row">
                                <input
                                  type={showBaiduOpenClientSecret ? 'text' : 'password'}
                                  value={baiduOpenCredentials.client_secret}
                                  onChange={(event) => setBaiduOpenCredentials((current) => ({ ...current, client_secret: event.target.value }))}
                                  placeholder={baiduOpenCredentials.client_secret_configured ? '已保存，留空保持不变' : ''}
                                  autoComplete="new-password"
                                  required={!baiduOpenCredentials.client_secret_configured}
                                />
                                <button type="button" className="ghost-button" onClick={() => setShowBaiduOpenClientSecret((current) => !current)}>{showBaiduOpenClientSecret ? '隐藏' : '显示'}</button>
                              </div>
                            </label>
                            <div className="button-row">
                              <button type="submit" disabled={baiduOpenCredentialsSaving}>{baiduOpenCredentialsSaving ? '保存中...' : '保存应用凭据'}</button>
                            </div>
                          </form>
                          <div className="hint">更换应用凭据会清除旧 Token。手动导入的 Token 必须由这里保存的同一套应用凭据签发。</div>
                        </section>

                        {baiduOpenAuthMode !== 'official' ? (
                          <section className="provider-auth-panel">
                            <div className="provider-section-heading">
                              <h3>OAuth Broker</h3>
                              <span>当前百度数据源独立使用的 Broker Client</span>
                            </div>
                            <form className="form-grid" onSubmit={handleSaveBaiduOpenBrokerConfig}>
                              <label className="form-field">
                                <span>Broker Base URL</span>
                                <input
                                  type="url"
                                  value={baiduOpenBrokerConfig.base_url}
                                  onChange={(event) => setBaiduOpenBrokerConfig((current) => ({ ...current, base_url: event.target.value }))}
                                  placeholder="https://auth.example.com"
                                  autoComplete="url"
                                  required
                                />
                              </label>
                              <label className="form-field">
                                <span>Broker Client ID</span>
                                <input
                                  value={baiduOpenBrokerConfig.client_id}
                                  onChange={(event) => setBaiduOpenBrokerConfig((current) => ({ ...current, client_id: event.target.value }))}
                                  autoComplete="off"
                                  required
                                />
                              </label>
                              <label className="form-field">
                                <span>Broker Token</span>
                                <div className="secret-input-row">
                                  <input
                                    type={showBaiduOpenBrokerToken ? 'text' : 'password'}
                                    value={baiduOpenBrokerConfig.token}
                                    onChange={(event) => setBaiduOpenBrokerConfig((current) => ({ ...current, token: event.target.value }))}
                                    placeholder={baiduOpenBrokerConfig.token_configured ? '已保存，留空保持不变' : ''}
                                    autoComplete="new-password"
                                    required={!baiduOpenBrokerConfig.token_configured}
                                  />
                                  <button type="button" className="ghost-button" onClick={() => setShowBaiduOpenBrokerToken((current) => !current)}>{showBaiduOpenBrokerToken ? '隐藏' : '显示'}</button>
                                </div>
                              </label>
                              <div className="button-row">
                                <button type="submit" disabled={baiduOpenBrokerSaving}>{baiduOpenBrokerSaving ? '保存中...' : '保存 Broker 配置'}</button>
                              </div>
                            </form>
                            {baiduOpenBrokerCallbackURI ? <div className="hint">百度应用的 Broker 回调：<code>{baiduOpenBrokerCallbackURI}</code></div> : null}
                          </section>
                        ) : null}
                        </div>

                        {baiduOpenAuthMode === 'official' ? (
                          <div className="auth-mode-body provider-auth-actions">
                            <div className="provider-section-heading">
                              <h3>账号授权</h3>
                              <span>由 NyaMedia 直接完成百度 OAuth</span>
                            </div>
                            <button type="button" onClick={() => handleStartBaiduOpenAuth('official')} disabled={baiduOpenAuthLoading}>{baiduOpenAuthLoading ? '等待授权...' : '打开百度官方授权页'}</button>
                            <div className="hint">在百度开放平台登记回调：<code>{baiduOpenCallbackURI || '请先配置 server.public_base_url'}</code></div>
                          </div>
                        ) : null}

                        {baiduOpenAuthMode === 'broker_relay' ? (
                          <div className="auth-mode-body provider-auth-actions">
                            <div className="provider-section-heading">
                              <h3>账号授权</h3>
                              <span>Broker 转发授权码，Token 由 NyaMedia 保存</span>
                            </div>
                            <button type="button" onClick={() => handleStartBaiduOpenAuth('broker_relay')} disabled={baiduOpenAuthLoading || !baiduOpenBrokerConfig.configured}>{baiduOpenAuthLoading ? '等待授权...' : '通过 Broker 授权'}</button>
                            <div className="hint">Broker Client 的 return_uri 白名单：<code>{baiduOpenCallbackURI || '请先配置 server.public_base_url'}</code></div>
                            {baiduOpenBrokerCallbackURI ? <div className="hint">百度开放平台登记回调：<code>{baiduOpenBrokerCallbackURI}</code></div> : null}
                          </div>
                        ) : null}

                        {baiduOpenAuthMode === 'broker_token_exchange' ? (
                          <div className="auth-mode-body provider-auth-actions">
                            <div className="provider-section-heading">
                              <h3>获取并导入 Token</h3>
                              <span>在 Broker 完成授权，再把结果复制回当前数据源</span>
                            </div>
                            <button type="button" onClick={() => handleStartBaiduOpenAuth('broker_token_exchange')} disabled={baiduOpenAuthLoading || !baiduOpenBrokerConfig.configured}>{baiduOpenAuthLoading ? '正在获取 Broker 地址...' : '打开 Broker 获取 Token'}</button>
                            <div className="hint">Broker 将在新标签打开；取得 Token 后回到此页导入。</div>
                            <form className="form-grid baidu-token-import" onSubmit={handleImportBaiduOpenTokens}>
                              <label className="form-field">
                                <span>Access Token</span>
                                <input
                                  type={showBaiduOpenTokens ? 'text' : 'password'}
                                  value={baiduOpenTokens.access_token}
                                  onChange={(event) => setBaiduOpenTokens((current) => ({ ...current, access_token: event.target.value }))}
                                  placeholder={baiduOpenTokens.access_token_configured ? '已保存，留空保持不变' : ''}
                                  autoComplete="off"
                                  required={!baiduOpenTokens.access_token_configured}
                                />
                              </label>
                              <label className="form-field">
                                <span>Refresh Token</span>
                                <input
                                  type={showBaiduOpenTokens ? 'text' : 'password'}
                                  value={baiduOpenTokens.refresh_token}
                                  onChange={(event) => setBaiduOpenTokens((current) => ({ ...current, refresh_token: event.target.value }))}
                                  placeholder={baiduOpenTokens.refresh_token_configured ? '已保存，留空保持不变' : ''}
                                  autoComplete="off"
                                  required={!baiduOpenTokens.refresh_token_configured}
                                />
                              </label>
                              <div className="button-row">
                                <button type="submit" disabled={baiduOpenTokenImportLoading || (!baiduOpenTokens.access_token.trim() && !baiduOpenTokens.refresh_token.trim())}>{baiduOpenTokenImportLoading ? '导入中...' : '导入 Token'}</button>
                                <button type="button" className="ghost-button" onClick={() => setShowBaiduOpenTokens((current) => !current)}>{showBaiduOpenTokens ? '隐藏 Token' : '显示 Token'}</button>
                              </div>
                            </form>
                          </div>
                        ) : null}

                        {baiduOpenAuth ? (
                          <div className="provider-auth-result">
                            <div className="hint">状态：{baiduOpenAuth.state}{baiduOpenAuth.message ? ` · ${baiduOpenAuth.message}` : ''}</div>
                            {baiduOpenAuth.authorization_url && baiduOpenAuth.state === 'pending' && baiduOpenAuth.mode !== 'broker_token_exchange' ? (
                              <a href={baiduOpenAuth.authorization_url} target="_blank" rel="noreferrer">重新打开授权页</a>
                            ) : null}
                          </div>
                        ) : null}
                      </div>
                    </section>
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
                      {!['123pan', 'baiduopen'].includes(providerForm.type) ? (
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
                      {providerForm.type === 'baiduopen' ? <div className="hint">专用表单保存应用凭据，OAuth 流程维护 <code>access_token</code>、<code>refresh_token</code> 和到期时间。</div> : null}
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
                                    {['123pan', 'baiduopen'].includes(providerForm.type) ? (
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
