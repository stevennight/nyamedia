import { useEffect, useMemo, useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
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

function toEmbyServerForm(server) {
  return {
    key: server?.key || '',
    name: server?.name || '',
    upstream_url: server?.upstream_url || '',
    api_key: server?.api_key || '',
    enabled: server ? Boolean(server.enabled) : true,
  }
}

function normalizeEmbyServer(server) {
  return {
    key: server?.key ?? server?.Key ?? '',
    name: server?.name ?? server?.Name ?? '',
    upstream_url: server?.upstream_url ?? server?.UpstreamURL ?? '',
    api_key: server?.api_key ?? server?.APIKey ?? '',
    enabled: server?.enabled ?? server?.Enabled ?? false,
    created_at: server?.created_at ?? server?.CreatedAt ?? '',
    updated_at: server?.updated_at ?? server?.UpdatedAt ?? '',
  }
}

function formatProxyBaseURL(serverKey) {
  if (!serverKey || typeof window === 'undefined') {
    return ''
  }
  return `${window.location.origin}/proxy/${serverKey}`
}

export function SettingsPage() {
  const [form, setForm] = useState({ key: '', value: '""' })
  const [accountForm, setAccountForm] = useState({ username: '', currentPassword: '', newPassword: '', confirmPassword: '' })
  const [accountError, setAccountError] = useState('')
  const [accountSuccess, setAccountSuccess] = useState('')
  const [accountSubmitting, setAccountSubmitting] = useState(false)
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
  const authState = useAsyncData(() => api.me(), [])
  const settingsMap = useMemo(() => normalizeSettings(settingsState.data), [settingsState.data])
  const isEditingEmbyServer = Boolean(selectedEmbyServerKey)

  useEffect(() => {
    if (!authState.data?.username) {
      return
    }
    setAccountForm((current) => (current.username ? current : { ...current, username: authState.data.username }))
  }, [authState.data])

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

  async function handleProxySave(event) {
    event.preventDefault()
    setProxyError('')
    setProxySuccess('')
    setProxySubmitting(true)

    try {
      const parsedRules = JSON.parse(proxyForm.userAgentRules || '[]')
      if (!Array.isArray(parsedRules)) {
        throw new Error('Web 客户端强制代理规则必须是 JSON 数组')
      }

      await Promise.all([
        api.upsertSetting('server.public_base_url', proxyForm.publicBaseURL.trim()),
        api.upsertSetting('playback.default_mode', proxyForm.defaultMode),
        api.upsertSetting('playback.token', proxyForm.token.trim()),
        api.upsertSetting('playback.user_agent_rules', parsedRules),
      ])
      setProxySuccess('Emby 反代配置已保存')
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
      <PageSection title="Emby Reverse Proxy">
        <form className="form-grid" onSubmit={handleProxySave}>
          <input value={proxyForm.publicBaseURL} onChange={(e) => setProxyForm({ ...proxyForm, publicBaseURL: e.target.value })} placeholder="public base url, e.g. https://media.example.com" />
          <select value={proxyForm.defaultMode} onChange={(e) => setProxyForm({ ...proxyForm, defaultMode: e.target.value })}>
            <option value="redirect">默认重定向</option>
            <option value="proxy">默认代理</option>
          </select>
          <input value={proxyForm.token} onChange={(e) => setProxyForm({ ...proxyForm, token: e.target.value })} placeholder="playback token (optional)" />
          <textarea value={proxyForm.userAgentRules} onChange={(e) => setProxyForm({ ...proxyForm, userAgentRules: e.target.value })} rows="8" placeholder='JSON array, e.g. ["Emby Web", "Mozilla/5.0"]' />
          <div className="hint-block">
            <strong>反代说明</strong>
            <p>公网地址用于生成可回写到 Emby 播放信息里的绝对流地址，强制代理规则使用 JSON 数组保存需要走 `proxy` 的客户端关键字。</p>
          </div>
          {proxyError ? <div className="banner banner-error">{proxyError}</div> : null}
          {proxySuccess ? <div className="banner banner-success">{proxySuccess}</div> : null}
          <div className="button-row">
            <button type="submit" disabled={proxySubmitting}>{proxySubmitting ? 'Saving...' : 'Save Proxy Config'}</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Emby Upstreams" actions={<><button className="ghost-button" onClick={handleCreateEmbyServer}>New Upstream</button><button onClick={embyServersState.refresh}>Refresh</button></>}>
        <StatusBanner error={embyServersState.error} loading={embyServersState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Key</th>
                  <th>Name</th>
                  <th>Upstream</th>
                  <th>Proxy Base</th>
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
                )) : <tr><td className="empty-cell" colSpan="5">还没有配置 Emby 反代上游</td></tr>}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>
      <PageSection title={isEditingEmbyServer ? 'Edit Emby Upstream' : 'Create Emby Upstream'}>
        <form className="form-grid" onSubmit={handleSaveEmbyServer}>
          <input value={embyServerForm.key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, key: e.target.value })} placeholder="server key, e.g. home-emby" disabled={isEditingEmbyServer} required />
          <input value={embyServerForm.name} onChange={(e) => setEmbyServerForm({ ...embyServerForm, name: e.target.value })} placeholder="display name" required />
          <input value={embyServerForm.upstream_url} onChange={(e) => setEmbyServerForm({ ...embyServerForm, upstream_url: e.target.value })} placeholder="upstream url, e.g. http://127.0.0.1:8096/emby" required />
          <input value={embyServerForm.api_key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, api_key: e.target.value })} placeholder="emby api key (optional)" />
          <label className="check-inline">
            <input type="checkbox" checked={embyServerForm.enabled} onChange={(e) => setEmbyServerForm({ ...embyServerForm, enabled: e.target.checked })} />
            <span>启用这个上游</span>
          </label>
          {(isEditingEmbyServer || embyServerForm.key) ? (
            <div className="hint-block compact">
              <strong>接入地址</strong>
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
