import { useMemo, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const emptySchedule = {
  id: '',
  name: '',
  library_id: '',
  mount_id: '',
  source_path: '',
  cron: '',
  enabled: true,
}

function normalizeSchedule(schedule) {
  return {
    id: schedule.id ?? schedule.ID ?? '',
    name: schedule.name ?? schedule.Name ?? '',
    library_id: schedule.library_id ?? schedule.LibraryID ?? '',
    mount_id: schedule.mount_id ?? schedule.MountID ?? '',
    source_path: schedule.source_path ?? schedule.SourcePath ?? '',
    cron: schedule.cron ?? schedule.Cron ?? '',
    enabled: schedule.enabled ?? schedule.Enabled ?? false,
    last_run_at: schedule.last_run_at ?? schedule.LastRunAt ?? '',
    created_at: schedule.created_at ?? schedule.CreatedAt ?? '',
    updated_at: schedule.updated_at ?? schedule.UpdatedAt ?? '',
  }
}

function normalizeLibrary(library) {
  return {
    id: library.id ?? library.ID ?? '',
    name: library.name ?? library.Name ?? '',
    enabled: library.enabled ?? library.Enabled ?? false,
  }
}

function normalizeMount(mount) {
  return {
    id: mount.id ?? mount.ID ?? '',
    library_id: mount.library_id ?? mount.LibraryID ?? '',
    provider_id: mount.provider_id ?? mount.ProviderID ?? '',
    source_path: mount.source_path ?? mount.SourcePath ?? '',
    enabled: mount.enabled ?? mount.Enabled ?? false,
  }
}

function scheduleToForm(schedule) {
  return {
    id: schedule.id,
    name: schedule.name,
    library_id: schedule.library_id,
    mount_id: schedule.mount_id,
    source_path: schedule.source_path,
    cron: schedule.cron,
    enabled: schedule.enabled,
  }
}

function schedulePayload(schedule) {
  return {
    name: schedule.name.trim(),
    library_id: schedule.library_id,
    mount_id: schedule.mount_id,
    source_path: schedule.source_path.trim(),
    cron: schedule.cron.trim(),
    enabled: schedule.enabled,
  }
}

function canonicalProviderPath(value) {
  const normalized = String(value || '').trim().replaceAll('\\', '/').replace(/\/+$/, '')
  return normalized || '/'
}

function providerPathWithinRoot(value, root) {
  const path = canonicalProviderPath(value)
  const rootPath = canonicalProviderPath(root)
  return rootPath === '/' ? path.startsWith('/') : path === rootPath || path.startsWith(`${rootPath}/`)
}

function filterDirectoryItems(items, query) {
  const keyword = query.trim().toLowerCase()
  if (!keyword) return items
  return items.filter((item) => `${item.name || ''} ${item.path || ''}`.toLowerCase().includes(keyword))
}

export function ScanSchedulesPage() {
  const { systemTimeZone } = useOutletContext() || {}
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingScheduleId, setEditingScheduleId] = useState('')
  const [scheduleForm, setScheduleForm] = useState(emptySchedule)
  const [mounts, setMounts] = useState([])
  const [mountsLoading, setMountsLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [togglingScheduleId, setTogglingScheduleId] = useState('')
  const [deletingScheduleId, setDeletingScheduleId] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [sourcePickerOpen, setSourcePickerOpen] = useState(false)
  const [sourceDirectoryState, setSourceDirectoryState] = useState(null)
  const [sourceDirectoryLoading, setSourceDirectoryLoading] = useState(false)
  const [sourceDirectoryError, setSourceDirectoryError] = useState('')
  const [sourceDirectoryFilter, setSourceDirectoryFilter] = useState('')
  const mountRequestRef = useRef(0)

  const schedulesState = useAsyncData(async () => {
    const response = await api.listScanSchedules()
    return (response.items || []).map(normalizeSchedule)
  }, [])
  const librariesState = useAsyncData(async () => {
    const response = await api.listLibraries()
    return (response.items || []).map(normalizeLibrary)
  }, [])

  const libraries = librariesState.data || []
  const libraryByID = useMemo(() => new Map(libraries.map((library) => [library.id, library])), [libraries])
  const selectedLibrary = libraryByID.get(scheduleForm.library_id) || null
  const selectedMount = mounts.find((mount) => mount.id === scheduleForm.mount_id) || null
  const hasLegacyScope = Boolean(editingScheduleId && (!scheduleForm.mount_id || !scheduleForm.source_path))
  const sourceDirectoryItems = sourceDirectoryState?.items || []
  const filteredSourceDirectoryItems = filterDirectoryItems(sourceDirectoryItems, sourceDirectoryFilter)
  const canBrowseParent = Boolean(
    selectedMount
    && sourceDirectoryState?.parent_path
    && providerPathWithinRoot(sourceDirectoryState.parent_path, selectedMount.source_path),
  )

  function resetMessages() {
    setActionMessage('')
    setActionError('')
  }

  async function loadMounts(libraryID, options = {}) {
    const requestID = mountRequestRef.current + 1
    mountRequestRef.current = requestID
    setMounts([])
    if (!libraryID) return

    setMountsLoading(true)
    try {
      const response = await api.listMounts(libraryID)
      if (mountRequestRef.current !== requestID) return
      const nextMounts = (response.items || []).map(normalizeMount)
      setMounts(nextMounts)
      if (options.selectFirst) {
        const firstEnabledMount = nextMounts.find((mount) => mount.enabled)
        setScheduleForm((current) => (
          current.library_id === libraryID
            ? { ...current, mount_id: firstEnabledMount?.id || '', source_path: firstEnabledMount?.source_path || '' }
            : current
        ))
      }
    } catch (error) {
      if (mountRequestRef.current === requestID) setActionError(error.message)
    } finally {
      if (mountRequestRef.current === requestID) setMountsLoading(false)
    }
  }

  function openCreateDialog() {
    resetMessages()
    const firstLibrary = libraries.find((library) => library.enabled) || null
    setEditingScheduleId('')
    setScheduleForm({ ...emptySchedule, library_id: firstLibrary?.id || '' })
    setMounts([])
    setDialogOpen(true)
    if (firstLibrary) loadMounts(firstLibrary.id, { selectFirst: true })
  }

  function openEditDialog(schedule) {
    resetMessages()
    setEditingScheduleId(schedule.id)
    setScheduleForm(scheduleToForm(schedule))
    setMounts([])
    setDialogOpen(true)
    loadMounts(schedule.library_id)
  }

  function closeDialog() {
    mountRequestRef.current += 1
    setDialogOpen(false)
    setEditingScheduleId('')
    setScheduleForm(emptySchedule)
    setMounts([])
    setMountsLoading(false)
    setSaving(false)
    closeSourceDirectoryPicker()
  }

  function handleLibraryChange(libraryID) {
    resetMessages()
    closeSourceDirectoryPicker()
    setScheduleForm((current) => ({ ...current, library_id: libraryID, mount_id: '', source_path: '' }))
    loadMounts(libraryID, { selectFirst: true })
  }

  function handleMountChange(mountID) {
    resetMessages()
    closeSourceDirectoryPicker()
    const mount = mounts.find((item) => item.id === mountID)
    setScheduleForm((current) => ({ ...current, mount_id: mountID, source_path: mount?.source_path || '' }))
  }

  async function handleSaveSchedule(event) {
    event.preventDefault()
    resetMessages()
    const payload = schedulePayload(scheduleForm)

    if (!payload.name) {
      setActionError('请输入计划名称。')
      return
    }
    if (!selectedLibrary) {
      setActionError('请选择一个有效的媒体库。')
      return
    }
    if (!selectedMount) {
      setActionError('请选择一个有效的媒体库映射。')
      return
    }
    if (payload.enabled && !selectedLibrary.enabled) {
      setActionError('启用计划前，需要先启用所选媒体库。')
      return
    }
    if (payload.enabled && !selectedMount.enabled) {
      setActionError('启用计划前，需要先启用所选媒体库映射。')
      return
    }
    if (!payload.source_path || !providerPathWithinRoot(payload.source_path, selectedMount.source_path)) {
      setActionError(`扫描目录必须位于映射源目录 ${selectedMount.source_path} 下。`)
      return
    }
    if (payload.cron.split(/\s+/).length !== 5) {
      setActionError('Cron 表达式需要包含 5 个字段。')
      return
    }

    setSaving(true)
    try {
      if (editingScheduleId) {
        await api.updateScanSchedule(editingScheduleId, payload)
      } else {
        await api.createScanSchedule(payload)
      }
      await schedulesState.refresh()
      const message = editingScheduleId ? `定时扫描 ${payload.name} 已保存。` : `定时扫描 ${payload.name} 已创建。`
      closeDialog()
      setActionMessage(message)
    } catch (error) {
      setActionError(error.message)
      setSaving(false)
    }
  }

  async function handleDeleteSchedule(schedule) {
    if (!window.confirm(`删除定时扫描 ${schedule.name || schedule.id}？`)) return
    resetMessages()
    setDeletingScheduleId(schedule.id)
    try {
      await api.deleteScanSchedule(schedule.id)
      await schedulesState.refresh()
      setActionMessage(`定时扫描 ${schedule.name || schedule.id} 已删除。`)
    } catch (error) {
      setActionError(error.message)
    } finally {
      setDeletingScheduleId('')
    }
  }

  async function handleToggleSchedule(schedule, enabled) {
    resetMessages()
    setTogglingScheduleId(schedule.id)
    try {
      await api.updateScanSchedule(schedule.id, schedulePayload({ ...schedule, enabled }))
      await schedulesState.refresh()
      setActionMessage(`定时扫描 ${schedule.name || schedule.id} 已${enabled ? '启用' : '停用'}。`)
    } catch (error) {
      setActionError(error.message)
    } finally {
      setTogglingScheduleId('')
    }
  }

  async function refreshAll() {
    resetMessages()
    await Promise.all([schedulesState.refresh(), librariesState.refresh()])
  }

  async function loadSourceDirectories(path = '', options = {}) {
    if (!selectedMount?.provider_id) {
      setSourceDirectoryError('请先选择可用映射。')
      return
    }
    const browsePath = path || selectedMount.source_path
    if (!providerPathWithinRoot(browsePath, selectedMount.source_path)) {
      setSourceDirectoryError(`只能浏览映射源目录 ${selectedMount.source_path} 及其子目录。`)
      return
    }

    setSourceDirectoryLoading(true)
    setSourceDirectoryError('')
    try {
      const response = await api.listProviderDirectories(selectedMount.provider_id, browsePath, options)
      setSourceDirectoryState(response)
      setSourceDirectoryFilter('')
    } catch (error) {
      setSourceDirectoryError(error.message)
    } finally {
      setSourceDirectoryLoading(false)
    }
  }

  function openSourceDirectoryPicker() {
    if (!selectedMount?.enabled) {
      setActionError('请先选择一个已启用的媒体库映射。')
      return
    }
    setSourcePickerOpen(true)
    setSourceDirectoryState(null)
    setSourceDirectoryFilter('')
    loadSourceDirectories(
      providerPathWithinRoot(scheduleForm.source_path, selectedMount.source_path)
        ? scheduleForm.source_path
        : selectedMount.source_path,
    )
  }

  function closeSourceDirectoryPicker() {
    setSourcePickerOpen(false)
    setSourceDirectoryState(null)
    setSourceDirectoryLoading(false)
    setSourceDirectoryError('')
    setSourceDirectoryFilter('')
  }

  function selectSourceDirectory() {
    const path = sourceDirectoryState?.path
    if (!path || !selectedMount || !providerPathWithinRoot(path, selectedMount.source_path)) return
    setScheduleForm((current) => ({ ...current, source_path: path }))
    closeSourceDirectoryPicker()
  }

  return (
    <div className="page-grid one-col">
      <PageSection
        title="定时扫描"
        actions={(
          <>
            <button type="button" className="ghost-button" onClick={refreshAll}>刷新</button>
            <button type="button" onClick={openCreateDialog} disabled={librariesState.loading || !libraries.some((library) => library.enabled)}>添加计划</button>
          </>
        )}
      >
        <StatusBanner error={schedulesState.error || librariesState.error || actionError} loading={schedulesState.loading || librariesState.loading}>
          <div className="table-wrap">
            <table className="data-table schedules-table">
              <thead>
                <tr>
                  <th>计划</th>
                  <th>媒体库</th>
                  <th>扫描范围</th>
                  <th>Cron</th>
                  <th>启用</th>
                  <th>上次触发</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {(schedulesState.data || []).map((schedule) => {
                  const library = libraryByID.get(schedule.library_id)
                  const legacyScope = !schedule.mount_id || !schedule.source_path
                  return (
                    <tr key={schedule.id}>
                      <td>
                        <div>{schedule.name || '未命名计划'}</div>
                        <div className="subtle-id">{schedule.id}</div>
                      </td>
                      <td>
                        <div>{library?.name || schedule.library_id || '-'}</div>
                        {library?.name ? <div className="subtle-id">{schedule.library_id}</div> : null}
                      </td>
                      <td className="schedule-scope break-cell">
                        <div>{legacyScope ? '整个媒体库（旧配置）' : schedule.source_path}</div>
                        {schedule.mount_id ? <div className="subtle-id">映射：{schedule.mount_id}</div> : null}
                      </td>
                      <td className="schedule-cron mono-text">{schedule.cron || '-'}</td>
                      <td>
                        <label className="check-inline schedule-toggle">
                          <input
                            type="checkbox"
                            checked={schedule.enabled}
                            disabled={togglingScheduleId === schedule.id}
                            onChange={(event) => handleToggleSchedule(schedule, event.target.checked)}
                            aria-label={`${schedule.enabled ? '停用' : '启用'} ${schedule.name || schedule.id}`}
                          />
                          {schedule.enabled ? '启用' : '停用'}
                        </label>
                      </td>
                      <td className="nowrap-cell">{formatLocalDateTime(schedule.last_run_at, systemTimeZone)}</td>
                      <td className="nowrap-cell">
                        <div className="button-row">
                          <button type="button" className="ghost-button" onClick={() => openEditDialog(schedule)}>编辑</button>
                          <button type="button" className="danger" disabled={deletingScheduleId === schedule.id} onClick={() => handleDeleteSchedule(schedule)}>
                            {deletingScheduleId === schedule.id ? '删除中' : '删除'}
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
                {(schedulesState.data || []).length === 0 ? (
                  <tr><td colSpan="7" className="empty-cell">暂无定时扫描计划。</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
          {actionMessage ? <div className="hint top-gap">{actionMessage}</div> : null}
        </StatusBanner>
      </PageSection>

      {dialogOpen ? (
        <div className="modal-backdrop" role="presentation">
          <div className="modal-card library-modal-card" role="dialog" aria-modal="true" aria-labelledby="scan-schedule-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="scan-schedule-dialog-title">{editingScheduleId ? '编辑定时扫描' : '添加定时扫描'}</h2>
                <p>{editingScheduleId ? '调整扫描范围、执行时间和启用状态。' : '为持续更新的目录创建独立扫描计划。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeDialog}>关闭</button>
            </div>

            <form className="form-grid top-gap" onSubmit={handleSaveSchedule}>
              <label className="form-field">
                <span>计划名称</span>
                <input value={scheduleForm.name} onChange={(event) => setScheduleForm({ ...scheduleForm, name: event.target.value })} placeholder="例如：每小时更新连载番剧" required />
              </label>
              <label className="form-field">
                <span>媒体库</span>
                <select value={scheduleForm.library_id} onChange={(event) => handleLibraryChange(event.target.value)} required>
                  <option value="">选择媒体库</option>
                  {scheduleForm.library_id && !selectedLibrary ? <option value={scheduleForm.library_id}>{scheduleForm.library_id}（不可用）</option> : null}
                  {libraries.filter((library) => library.enabled || library.id === scheduleForm.library_id).map((library) => (
                    <option key={library.id} value={library.id}>{library.name}{library.enabled ? '' : '（已停用）'}</option>
                  ))}
                </select>
              </label>
              <label className="form-field">
                <span>媒体库映射</span>
                <select value={scheduleForm.mount_id} onChange={(event) => handleMountChange(event.target.value)} disabled={!scheduleForm.library_id || mountsLoading} required>
                  <option value="">{mountsLoading ? '正在读取映射...' : '选择映射'}</option>
                  {scheduleForm.mount_id && !selectedMount ? <option value={scheduleForm.mount_id}>{scheduleForm.mount_id}（不可用）</option> : null}
                  {mounts.filter((mount) => mount.enabled || mount.id === scheduleForm.mount_id).map((mount) => (
                    <option key={mount.id} value={mount.id}>
                      {mount.provider_id} · {mount.source_path}{mount.enabled ? '' : '（已停用）'}
                    </option>
                  ))}
                </select>
              </label>
              <label className="form-field">
                <span>扫描目录</span>
                <div className="path-input-row">
                  <input value={scheduleForm.source_path} onChange={(event) => setScheduleForm({ ...scheduleForm, source_path: event.target.value })} placeholder="选择映射内持续更新的目录" required />
                  <button type="button" className="ghost-button" onClick={openSourceDirectoryPicker} disabled={!selectedMount?.enabled}>浏览</button>
                </div>
              </label>
              <label className="form-field">
                <span>Cron</span>
                <input className="mono-text" value={scheduleForm.cron} onChange={(event) => setScheduleForm({ ...scheduleForm, cron: event.target.value })} placeholder="例如 0 * * * *" required />
              </label>
              <label className="check-inline"><input type="checkbox" checked={scheduleForm.enabled} onChange={(event) => setScheduleForm({ ...scheduleForm, enabled: event.target.checked })} /> 启用计划</label>

              {hasLegacyScope ? <div className="banner">这是旧的全库扫描计划。保存前需要选择映射和具体扫描目录。</div> : null}
              {actionError ? <div className="banner banner-error">{actionError}</div> : null}
              <div className="button-row">
                <button type="submit" disabled={saving || mountsLoading}>{saving ? '保存中...' : editingScheduleId ? '保存计划' : '创建计划'}</button>
              </div>
            </form>
          </div>
        </div>
      ) : null}

      {sourcePickerOpen ? (
        <div className="modal-backdrop nested-modal" role="presentation" onClick={closeSourceDirectoryPicker}>
          <div className="modal-card directory-picker-card" role="dialog" aria-modal="true" aria-labelledby="schedule-source-directory-picker-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="schedule-source-directory-picker-title">选择扫描目录</h2>
                <p>当前范围限定在所选媒体库映射内。</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeSourceDirectoryPicker}>关闭</button>
            </div>

            <div className="directory-toolbar top-gap">
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(selectedMount?.source_path)} disabled={sourceDirectoryLoading}>映射根目录</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.parent_path)} disabled={!canBrowseParent || sourceDirectoryLoading}>上级目录</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.path)} disabled={!sourceDirectoryState?.path || sourceDirectoryLoading}>刷新</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.path, { force: true })} disabled={!sourceDirectoryState?.path || sourceDirectoryLoading}>强制刷新</button>
            </div>

            <div className="directory-current mono-text top-gap">
              {sourceDirectoryState?.path || '正在加载...'}
              {selectedMount ? <span className="directory-root-hint">映射根目录：{selectedMount.source_path}</span> : null}
            </div>

            {sourceDirectoryError ? <div className="banner banner-error top-gap">{sourceDirectoryError}</div> : null}
            {sourceDirectoryLoading ? <div className="hint top-gap">正在读取目录...</div> : null}

            <div className="directory-filter top-gap">
              <input value={sourceDirectoryFilter} onChange={(event) => setSourceDirectoryFilter(event.target.value)} placeholder="搜索当前目录下的子目录" />
            </div>

            <div className="directory-list top-gap">
              {filteredSourceDirectoryItems.map((item) => (
                <button type="button" className="directory-item" key={item.path} onClick={() => loadSourceDirectories(item.path)}>
                  <span>{item.name}</span>
                  <code>{item.path}</code>
                </button>
              ))}
              {!sourceDirectoryLoading && sourceDirectoryItems.length === 0 ? <div className="empty-cell">当前目录下没有子目录。</div> : null}
              {!sourceDirectoryLoading && sourceDirectoryItems.length > 0 && filteredSourceDirectoryItems.length === 0 ? <div className="empty-cell">没有匹配的子目录。</div> : null}
            </div>

            <div className="button-row top-gap">
              <button type="button" onClick={selectSourceDirectory} disabled={!sourceDirectoryState?.path || sourceDirectoryLoading}>选择当前目录</button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
