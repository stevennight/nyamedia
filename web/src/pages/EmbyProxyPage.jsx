import { useEffect, useMemo, useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyProxyForm = {
  publicBaseURL: '',
  defaultMode: 'redirect',
  token: '',
  userAgentRules: '[]',
}

const emptyEmbyServerForm = {
  key: '',
  name: '',
  upstream_url: '',
  api_key: '',
  enabled: true,
}

function parseSettingValue(valueJSON, fallback = '') {
  if (typeof valueJSON !== 'string') {
    return fallback
  }

  try {
    return JSON.parse(valueJSON)
  } catch {
    return fallback
  }
}

function normalizeSettings(items) {
  const next = {}
  for (const item of items || []) {
    const key = item.key || item.Key
    if (!key) {
      continue
    }
    next[key] = parseSettingValue(item.value_json ?? item.ValueJSON)
  }
  return next
}

function buildProxyForm(settingsMap) {
  const userAgentRules = settingsMap['playback.user_agent_rules']
  return {
    publicBaseURL: String(settingsMap['server.public_base_url'] || ''),
    defaultMode: settingsMap['playback.default_mode'] === 'proxy' ? 'proxy' : 'redirect',
    token: String(settingsMap['playback.token'] || ''),
    userAgentRules: JSON.stringify(Array.isArray(userAgentRules) ? userAgentRules : [], null, 2),
  }
}

function normalizeEmbyServer(server) {
  return {
    key: server?.key ?? server?.Key ?? '',
    name: server?.name ?? server?.Name ?? '',
    upstream_url: server?.upstream_url ?? server?.UpstreamURL ?? '',
    api_key: server?.api_key ?? server?.APIKey ?? '',
    enabled: server?.enabled ?? server?.Enabled ?? false,
  }
}

function toEmbyServerForm(server) {
  return {
    key: server?.key || '',
    name: server?.name || '',
    upstream_url: server?.upstream_url || '',
    api_key: server?.api_key || '',
    enabled: server ? Boolean(server.enabled) : true,
  }
}

function formatProxyBaseURL(serverKey) {
  if (!serverKey || typeof window === 'undefined') {
    return ''
  }
  return `${window.location.origin}/proxy/${serverKey}`
}

export function EmbyProxyPage() {
  const [proxyForm, setProxyForm] = useState(emptyProxyForm)
  const [proxyError, setProxyError] = useState('')
  const [proxySuccess, setProxySuccess] = useState('')
  const [proxySubmitting, setProxySubmitting] = useState(false)
  const [selectedEmbyServerKey, setSelectedEmbyServerKey] = useState('')
  const [embyServerForm, setEmbyServerForm] = useState(emptyEmbyServerForm)
  const [embyServerError, setEmbyServerError] = useState('')
  const [embyServerSuccess, setEmbyServerSuccess] = useState('')
  const [embyServerSubmitting, setEmbyServerSubmitting] = useState(false)
  const settingsState = useAsyncData(async () => (await api.listSettings()).items || [], [])
  const embyServersState = useAsyncData(async () => ((await api.listEmbyServers()).items || []).map(normalizeEmbyServer), [])
  const settingsMap = useMemo(() => normalizeSettings(settingsState.data), [settingsState.data])
  const isEditingEmbyServer = Boolean(selectedEmbyServerKey)

  useEffect(() => {
    setProxyForm(buildProxyForm(settingsMap))
  }, [settingsMap])

  useEffect(() => {
    const servers = embyServersState.data || []
    if (!servers.length) {
      setSelectedEmbyServerKey('')
      setEmbyServerForm(emptyEmbyServerForm)
      return
    }

    const selected = servers.find((item) => item.key === selectedEmbyServerKey)
    if (selected) {
      setEmbyServerForm(toEmbyServerForm(selected))
      return
    }

    setSelectedEmbyServerKey(servers[0].key)
    setEmbyServerForm(toEmbyServerForm(servers[0]))
  }, [embyServersState.data, selectedEmbyServerKey])

  async function handleProxySave(event) {
    event.preventDefault()
    setProxyError('')
    setProxySuccess('')
    setProxySubmitting(true)

    try {
      const parsedRules = JSON.parse(proxyForm.userAgentRules || '[]')
      if (!Array.isArray(parsedRules)) {
        throw new Error('强制代理规则必须是 JSON 数组')
      }

      await Promise.all([
        api.upsertSetting('server.public_base_url', proxyForm.publicBaseURL.trim()),
        api.upsertSetting('playback.default_mode', proxyForm.defaultMode),
        api.upsertSetting('playback.token', proxyForm.token.trim()),
        api.upsertSetting('playback.user_agent_rules', parsedRules),
      ])
      setProxySuccess('播放网关配置已保存')
      settingsState.refresh()
    } catch (err) {
      setProxyError(err instanceof Error ? err.message : String(err))
    } finally {
      setProxySubmitting(false)
    }
  }

  function handleCreateEmbyServer() {
    setSelectedEmbyServerKey('')
    setEmbyServerForm(emptyEmbyServerForm)
    setEmbyServerError('')
    setEmbyServerSuccess('')
  }

  function handleSelectEmbyServer(server) {
    setSelectedEmbyServerKey(server.key)
    setEmbyServerForm(toEmbyServerForm(server))
    setEmbyServerError('')
    setEmbyServerSuccess('')
  }

  async function handleSaveEmbyServer(event) {
    event.preventDefault()
    setEmbyServerError('')
    setEmbyServerSuccess('')
    setEmbyServerSubmitting(true)

    try {
      if (selectedEmbyServerKey) {
        await api.updateEmbyServer(selectedEmbyServerKey, embyServerForm)
        setEmbyServerSuccess('Emby 上游已更新')
      } else {
        const created = normalizeEmbyServer(await api.createEmbyServer(embyServerForm))
        setSelectedEmbyServerKey(created.key)
        setEmbyServerForm(toEmbyServerForm(created))
        setEmbyServerSuccess('Emby 上游已创建')
      }
      embyServersState.refresh()
    } catch (err) {
      setEmbyServerError(err instanceof Error ? err.message : String(err))
    } finally {
      setEmbyServerSubmitting(false)
    }
  }

  async function handleDeleteEmbyServer() {
    if (!selectedEmbyServerKey) {
      return
    }
    if (!window.confirm(`确认删除 Emby 上游 ${selectedEmbyServerKey} 吗？`)) {
      return
    }

    setEmbyServerError('')
    setEmbyServerSuccess('')
    setEmbyServerSubmitting(true)
    try {
      await api.deleteEmbyServer(selectedEmbyServerKey)
      setSelectedEmbyServerKey('')
      setEmbyServerForm(emptyEmbyServerForm)
      setEmbyServerSuccess('Emby 上游已删除')
      embyServersState.refresh()
    } catch (err) {
      setEmbyServerError(err instanceof Error ? err.message : String(err))
    } finally {
      setEmbyServerSubmitting(false)
    }
  }

  return (
    <div className="page-grid two-col">
      <PageSection title="用途说明">
        <div className="hint-block">
          <strong>这个页面不是给 Emby115 自己做反代</strong>
          <p>它是用来管理“上游 Emby/Jellyfin 服务器接入 + 播放地址改写”这套能力。你把已有 Emby 挂到这里后，客户端访问 `/proxy/&lt;key&gt;`，服务会把 Emby 返回的播放地址改写成 Emby115 的 `/stream/...` 地址，这样就能统一走直链或代理策略。</p>
        </div>
        <div className="hint-block compact top-gap">
          <strong>适合的场景</strong>
          <p>你已经有一个 Emby，希望继续保留它的库和刮削能力，但播放时改为走 Emby115 的网盘直链 / 代理网关。</p>
        </div>
      </PageSection>
      <PageSection title="播放网关设置">
        <form className="form-grid" onSubmit={handleProxySave}>
          <input value={proxyForm.publicBaseURL} onChange={(e) => setProxyForm({ ...proxyForm, publicBaseURL: e.target.value })} placeholder="公网访问地址，例如 https://media.example.com" />
          <select value={proxyForm.defaultMode} onChange={(e) => setProxyForm({ ...proxyForm, defaultMode: e.target.value })}>
            <option value="redirect">默认直链跳转</option>
            <option value="proxy">默认由本服务代理转发</option>
          </select>
          <input value={proxyForm.token} onChange={(e) => setProxyForm({ ...proxyForm, token: e.target.value })} placeholder="播放 token，可选" />
          <textarea value={proxyForm.userAgentRules} onChange={(e) => setProxyForm({ ...proxyForm, userAgentRules: e.target.value })} rows="8" placeholder='强制代理客户端规则，JSON 数组，例如 ["Emby Web", "Mozilla/5.0"]' />
          <div className="hint-block compact">
            <strong>字段说明</strong>
            <p>`public_base_url` 用于给 Emby 回写绝对播放地址。`default_mode` 控制默认走直链还是代理。`user_agent_rules` 里命中的客户端会强制走代理。</p>
          </div>
          {proxyError ? <div className="banner banner-error">{proxyError}</div> : null}
          {proxySuccess ? <div className="banner banner-success">{proxySuccess}</div> : null}
          <div className="button-row">
            <button type="submit" disabled={proxySubmitting}>{proxySubmitting ? 'Saving...' : 'Save Gateway Settings'}</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Emby 上游列表" actions={<><button className="ghost-button" onClick={handleCreateEmbyServer}>New Upstream</button><button onClick={embyServersState.refresh}>Refresh</button></>}>
        <StatusBanner error={embyServersState.error} loading={embyServersState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Name</th>
                  <th>Upstream</th>
                  <th>接入地址</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {(embyServersState.data || []).length ? (embyServersState.data || []).map((server) => (
                  <tr key={server.key} className={server.key === selectedEmbyServerKey ? 'row-selected' : ''} onClick={() => handleSelectEmbyServer(server)}>
                    <td className="mono-text">{server.key}</td>
                    <td>{server.name}</td>
                    <td className="mono-text">{server.upstream_url}</td>
                    <td className="mono-text">{formatProxyBaseURL(server.key)}</td>
                    <td>{server.enabled ? 'Enabled' : 'Disabled'}</td>
                  </tr>
                )) : <tr><td className="empty-cell" colSpan="5">还没有配置 Emby 上游</td></tr>}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>
      <PageSection title={isEditingEmbyServer ? '编辑 Emby 上游' : '新建 Emby 上游'}>
        <form className="form-grid" onSubmit={handleSaveEmbyServer}>
          <input value={embyServerForm.key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, key: e.target.value })} placeholder="唯一标识，例如 home-emby" disabled={isEditingEmbyServer} required />
          <input value={embyServerForm.name} onChange={(e) => setEmbyServerForm({ ...embyServerForm, name: e.target.value })} placeholder="显示名称" required />
          <input value={embyServerForm.upstream_url} onChange={(e) => setEmbyServerForm({ ...embyServerForm, upstream_url: e.target.value })} placeholder="上游 Emby 地址，例如 http://127.0.0.1:8096/emby" required />
          <input value={embyServerForm.api_key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, api_key: e.target.value })} placeholder="Emby API Key，可选" />
          <label className="check-inline">
            <input type="checkbox" checked={embyServerForm.enabled} onChange={(e) => setEmbyServerForm({ ...embyServerForm, enabled: e.target.checked })} />
            <span>启用这个 Emby 上游</span>
          </label>
          {(isEditingEmbyServer || embyServerForm.key) ? (
            <div className="hint-block compact">
              <strong>客户端接入地址</strong>
              <p className="mono-text">{formatProxyBaseURL(embyServerForm.key) || '保存后可用'}</p>
            </div>
          ) : null}
          {embyServerError ? <div className="banner banner-error">{embyServerError}</div> : null}
          {embyServerSuccess ? <div className="banner banner-success">{embyServerSuccess}</div> : null}
          <div className="button-row">
            <button type="submit" disabled={embyServerSubmitting}>{embyServerSubmitting ? 'Saving...' : isEditingEmbyServer ? 'Update Upstream' : 'Create Upstream'}</button>
            {isEditingEmbyServer ? <button type="button" className="danger" disabled={embyServerSubmitting} onClick={handleDeleteEmbyServer}>Delete</button> : null}
          </div>
        </form>
      </PageSection>
    </div>
  )
}
