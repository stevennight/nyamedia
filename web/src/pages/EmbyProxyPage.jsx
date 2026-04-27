import { useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyEmbyServerForm = {
  key: '',
  name: '',
  upstream_url: '',
  api_key: '',
  enabled: true,
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
  const [dialogOpen, setDialogOpen] = useState(false)
  const [dialogMode, setDialogMode] = useState('create')
  const [embyServerForm, setEmbyServerForm] = useState(emptyEmbyServerForm)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const embyServersState = useAsyncData(async () => ((await api.listEmbyServers()).items || []).map(normalizeEmbyServer), [])
  const isEditing = dialogMode === 'edit'

  function resetDialogState() {
    setEmbyServerForm(emptyEmbyServerForm)
    setMessage('')
    setError('')
    setSubmitting(false)
  }

  function openCreateDialog() {
    resetDialogState()
    setDialogMode('create')
    setDialogOpen(true)
  }

  function openEditDialog(server) {
    setEmbyServerForm(toEmbyServerForm(server))
    setMessage('')
    setError('')
    setDialogMode('edit')
    setDialogOpen(true)
  }

  function closeDialog() {
    setDialogOpen(false)
    resetDialogState()
  }

  async function handleSaveEmbyServer(event) {
    event.preventDefault()
    setError('')
    setMessage('')
    setSubmitting(true)

    try {
      if (isEditing) {
        const updated = normalizeEmbyServer(await api.updateEmbyServer(embyServerForm.key, embyServerForm))
        setEmbyServerForm(toEmbyServerForm(updated))
        setMessage('Emby 上游已更新')
      } else {
        const created = normalizeEmbyServer(await api.createEmbyServer(embyServerForm))
        setDialogMode('edit')
        setEmbyServerForm(toEmbyServerForm(created))
        setMessage('Emby 上游已创建')
      }
      embyServersState.refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDeleteEmbyServer(key = embyServerForm.key) {
    if (!key) {
      return
    }
    if (!window.confirm(`确认删除 Emby 上游 ${key} 吗？`)) {
      return
    }

    setError('')
    setMessage('')
    setSubmitting(true)
    try {
      await api.deleteEmbyServer(key)
      embyServersState.refresh()
      if (dialogOpen) {
        closeDialog()
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="Emby 上游" actions={<><button type="button" onClick={embyServersState.refresh}>刷新</button><button type="button" onClick={openCreateDialog}>新建上游</button></>}>
        <div className="hint-block compact">
          <strong>用途说明</strong>
          <p>把已有 Emby/Jellyfin 接入到 NyaMedia，客户端使用接入地址访问，播放时可由 NyaMedia 改写为网盘直链或代理网关地址。</p>
        </div>
        {error && !dialogOpen ? <div className="banner banner-error top-gap">{error}</div> : null}
        <StatusBanner error={embyServersState.error} loading={embyServersState.loading}>
          <div className="table-wrap top-gap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>标识</th>
                  <th>名称</th>
                  <th>上游地址</th>
                  <th>接入地址</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {(embyServersState.data || []).map((server) => (
                  <tr key={server.key}>
                    <td className="mono-text">{server.key}</td>
                    <td>{server.name}</td>
                    <td className="mono-text">{server.upstream_url}</td>
                    <td className="mono-text">{formatProxyBaseURL(server.key)}</td>
                    <td>{server.enabled ? '已启用' : '已禁用'}</td>
                    <td>
                      <div className="button-row">
                        <button type="button" onClick={() => openEditDialog(server)}>编辑</button>
                        <button type="button" className="danger" onClick={() => handleDeleteEmbyServer(server.key)}>删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {(embyServersState.data || []).length === 0 ? <tr><td className="empty-cell" colSpan="6">还没有配置 Emby 上游。</td></tr> : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>

      {dialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeDialog}>
          <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="emby-server-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="emby-server-dialog-title">{isEditing ? '编辑 Emby 上游' : '新建 Emby 上游'}</h2>
                <p>{isEditing ? `管理上游 ${embyServerForm.key}。` : '创建后可在客户端使用接入地址访问这个上游。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeDialog}>关闭</button>
            </div>

            <form className="form-grid" onSubmit={handleSaveEmbyServer}>
              <input value={embyServerForm.key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, key: e.target.value })} placeholder="唯一标识，例如 home-emby" disabled={isEditing} required />
              <input value={embyServerForm.name} onChange={(e) => setEmbyServerForm({ ...embyServerForm, name: e.target.value })} placeholder="显示名称" required />
              <input value={embyServerForm.upstream_url} onChange={(e) => setEmbyServerForm({ ...embyServerForm, upstream_url: e.target.value })} placeholder="上游 Emby 地址，例如 http://127.0.0.1:8096/emby" required />
              <input value={embyServerForm.api_key} onChange={(e) => setEmbyServerForm({ ...embyServerForm, api_key: e.target.value })} placeholder="Emby API Key，可选" />
              <label className="check-inline">
                <input type="checkbox" checked={embyServerForm.enabled} onChange={(e) => setEmbyServerForm({ ...embyServerForm, enabled: e.target.checked })} />
                <span>启用这个 Emby 上游</span>
              </label>
              {(isEditing || embyServerForm.key) ? (
                <div className="hint-block compact">
                  <strong>客户端接入地址</strong>
                  <p className="mono-text">{formatProxyBaseURL(embyServerForm.key) || '保存后可用'}</p>
                </div>
              ) : null}
              {error ? <div className="banner banner-error">{error}</div> : null}
              {message ? <div className="banner banner-success">{message}</div> : null}
              <div className="button-row">
                <button type="submit" disabled={submitting}>{submitting ? '保存中...' : isEditing ? '保存上游' : '创建上游'}</button>
                {isEditing ? <button type="button" className="danger" disabled={submitting} onClick={() => handleDeleteEmbyServer()}>删除上游</button> : null}
              </div>
            </form>
          </div>
        </div>
      ) : null}
    </div>
  )
}
