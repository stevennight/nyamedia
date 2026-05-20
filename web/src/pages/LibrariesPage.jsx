import { useMemo, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const emptyLibrary = { id: '', name: '', description: '', scan_cron: '', enabled: true }
const emptyMount = { id: '', provider_id: '', source_path: '', target_path: '', media_type: '', priority: 100, enabled: true }

function createPartialScanPath(path = '') {
  return { id: crypto.randomUUID(), path, error: '' }
}

function normalizeLibrary(library) {
  return {
    id: library.id ?? library.ID ?? '',
    name: library.name ?? library.Name ?? '',
    description: library.description ?? library.Description ?? '',
    enabled: library.enabled ?? library.Enabled ?? false,
    last_scan_at: library.last_scan_at ?? library.LastScanAt ?? '',
    scan_cron: library.scan_cron ?? library.ScanCron ?? '',
    created_at: library.created_at ?? library.CreatedAt ?? '',
    updated_at: library.updated_at ?? library.UpdatedAt ?? '',
  }
}

function normalizeMount(mount) {
  return {
    id: mount.id ?? mount.ID ?? '',
    library_id: mount.library_id ?? mount.LibraryID ?? '',
    provider_id: mount.provider_id ?? mount.ProviderID ?? '',
    source_path: mount.source_path ?? mount.SourcePath ?? '',
    target_path: mount.target_path ?? mount.TargetPath ?? '',
    media_type: mount.media_type ?? mount.MediaType ?? '',
    priority: mount.priority ?? mount.Priority ?? 100,
    enabled: mount.enabled ?? mount.Enabled ?? false,
  }
}

function normalizeProvider(provider) {
  return {
    id: provider.id ?? provider.ID ?? '',
    name: provider.name ?? provider.Name ?? '',
  }
}

function filterDirectoryItems(items, query) {
  const keyword = query.trim().toLowerCase()
  if (!keyword) {
    return items
  }
  return items.filter((item) => `${item.name || ''} ${item.path || ''}`.toLowerCase().includes(keyword))
}

function createLibraryDraft() {
  return {
    ...emptyLibrary,
    id: crypto.randomUUID(),
  }
}

function createMountDraft() {
  return {
    ...emptyMount,
    id: crypto.randomUUID(),
  }
}

function libraryToForm(library) {
  return {
    id: library.id,
    name: library.name,
    description: library.description || '',
    scan_cron: library.scan_cron || '',
    enabled: library.enabled,
  }
}

function mountToForm(mount) {
  return {
    id: mount.id,
    provider_id: mount.provider_id,
    source_path: mount.source_path,
    target_path: mount.target_path,
    priority: mount.priority || 100,
    enabled: mount.enabled,
  }
}

function mountToPayload(mount, overrides = {}) {
  return {
    id: mount.id,
    provider_id: mount.provider_id,
    source_path: mount.source_path,
    target_path: mount.target_path,
    media_type: mount.media_type || '',
    priority: mount.priority || 100,
    enabled: mount.enabled,
    ...overrides,
  }
}

function reorderItems(items, fromId, toId) {
  const fromIndex = items.findIndex((item) => item.id === fromId)
  const toIndex = items.findIndex((item) => item.id === toId)
  if (fromIndex === -1 || toIndex === -1 || fromIndex === toIndex) {
    return items
  }

  const next = [...items]
  const [moved] = next.splice(fromIndex, 1)
  next.splice(toIndex, 0, moved)
  return next
}

async function loadLibrariesWithSummary() {
  const libraries = ((await api.listLibraries()).items || []).map(normalizeLibrary)
  const mountsByLibrary = await Promise.all(
    libraries.map(async (library) => ({
      id: library.id,
      mounts: ((await api.listMounts(library.id)).items || []).map(normalizeMount),
    })),
  )

  const summaryByLibrary = new Map(
    mountsByLibrary.map(({ id, mounts }) => [
      id,
      {
        mountCount: mounts.length,
        enabledMountCount: mounts.filter((mount) => mount.enabled).length,
      },
    ]),
  )

  return libraries.map((library) => ({
    ...library,
    ...(summaryByLibrary.get(library.id) || { mountCount: 0, enabledMountCount: 0 }),
  }))
}

export function LibrariesPage() {
  const { systemTimeZone } = useOutletContext() || {}
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [mappingsDialogOpen, setMappingsDialogOpen] = useState(false)
  const [partialScanDialogOpen, setPartialScanDialogOpen] = useState(false)
  const [mappingFormDialogOpen, setMappingFormDialogOpen] = useState(false)
  const [selectedLibraryId, setSelectedLibraryId] = useState('')
  const [partialScanLibrary, setPartialScanLibrary] = useState(null)
  const [partialScanMounts, setPartialScanMounts] = useState([])
  const [partialScanMountsLoading, setPartialScanMountsLoading] = useState(false)
  const [partialScanSubmitting, setPartialScanSubmitting] = useState(false)
  const [partialScanMountId, setPartialScanMountId] = useState('')
  const [partialScanSourcePaths, setPartialScanSourcePaths] = useState([createPartialScanPath()])
  const [partialScanSubmittedPaths, setPartialScanSubmittedPaths] = useState([])
  const [libraryForm, setLibraryForm] = useState(emptyLibrary)
  const [mountForm, setMountForm] = useState(emptyMount)
  const [editingMountId, setEditingMountId] = useState('')
  const [draggedMountId, setDraggedMountId] = useState('')
  const [dropTargetMountId, setDropTargetMountId] = useState('')
  const [overwriteScanOutputs, setOverwriteScanOutputs] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [outputPickerOpen, setOutputPickerOpen] = useState(false)
  const [outputPickerTarget, setOutputPickerTarget] = useState('')
  const [outputDirectoryState, setOutputDirectoryState] = useState(null)
  const [outputDirectoryLoading, setOutputDirectoryLoading] = useState(false)
  const [outputDirectoryError, setOutputDirectoryError] = useState('')
  const [newOutputDirectoryName, setNewOutputDirectoryName] = useState('')
  const [sourcePickerOpen, setSourcePickerOpen] = useState(false)
  const [sourcePickerTarget, setSourcePickerTarget] = useState('mount')
  const [sourcePickerPartialPathId, setSourcePickerPartialPathId] = useState('')
  const [sourceDirectoryState, setSourceDirectoryState] = useState(null)
  const [sourceDirectoryLoading, setSourceDirectoryLoading] = useState(false)
  const [sourceDirectoryError, setSourceDirectoryError] = useState('')
  const [sourceDirectoryFilter, setSourceDirectoryFilter] = useState('')
  const [outputDirectoryFilter, setOutputDirectoryFilter] = useState('')

  const librariesState = useAsyncData(loadLibrariesWithSummary, [])
  const providersState = useAsyncData(async () => ((await api.listProviders()).items || []).map(normalizeProvider), [])
  const mountsState = useAsyncData(async () => {
    if (!selectedLibraryId) {
      return []
    }
    return ((await api.listMounts(selectedLibraryId)).items || []).map(normalizeMount)
  }, [selectedLibraryId])

  const selectedLibrary = useMemo(
    () => (librariesState.data || []).find((library) => library.id === selectedLibraryId) || null,
    [librariesState.data, selectedLibraryId],
  )

  const selectedPartialScanMount = useMemo(
    () => partialScanMounts.find((mount) => mount.id === partialScanMountId) || null,
    [partialScanMountId, partialScanMounts],
  )

  function resetMessages() {
    setActionMessage('')
    setActionError('')
  }

  function openCreateDialog() {
    resetMessages()
    setLibraryForm(createLibraryDraft())
    setCreateDialogOpen(true)
  }

  function closeCreateDialog() {
    setCreateDialogOpen(false)
    setLibraryForm(emptyLibrary)
  }

  function openEditDialog(library) {
    resetMessages()
    setSelectedLibraryId(library.id)
    setLibraryForm(libraryToForm(library))
    setEditDialogOpen(true)
  }

  function closeEditDialog() {
    setEditDialogOpen(false)
  }

  function openMappingsDialog(library) {
    resetMessages()
    setSelectedLibraryId(library.id)
    setMappingsDialogOpen(true)
    setMappingFormDialogOpen(false)
    setEditingMountId('')
    setMountForm(createMountDraft())
  }

  function closeMappingsDialog() {
    setMappingsDialogOpen(false)
    setMappingFormDialogOpen(false)
    setEditingMountId('')
    setDraggedMountId('')
    setDropTargetMountId('')
    setMountForm(emptyMount)
    closeOutputDirectoryPicker()
    closeSourceDirectoryPicker()
  }

  async function openPartialScanDialog(library) {
    resetMessages()
    setSelectedLibraryId(library.id)
    setPartialScanLibrary(library)
    setPartialScanDialogOpen(true)
    setPartialScanMounts([])
    setPartialScanMountId('')
    setPartialScanSourcePaths([createPartialScanPath()])
    setPartialScanSubmittedPaths([])
    setPartialScanSubmitting(false)
    setPartialScanMountsLoading(true)
    try {
      const mounts = ((await api.listMounts(library.id)).items || []).map(normalizeMount).filter((mount) => mount.enabled)
      setPartialScanMounts(mounts)
      if (mounts[0]) {
        setPartialScanMountId(mounts[0].id)
        setPartialScanSourcePaths([createPartialScanPath(mounts[0].source_path)])
      }
      if (mounts.length === 0) {
        setActionError('该媒体库没有启用映射，无法局部扫描。')
      }
    } catch (error) {
      setActionError(error.message)
    } finally {
      setPartialScanMountsLoading(false)
    }
  }

  function closePartialScanDialog() {
    setPartialScanDialogOpen(false)
    setPartialScanLibrary(null)
    setPartialScanMounts([])
    setPartialScanMountId('')
    setPartialScanSourcePaths([createPartialScanPath()])
    setPartialScanSubmittedPaths([])
    setPartialScanSubmitting(false)
    closeSourceDirectoryPicker()
  }

  function openCreateMappingDialog() {
    resetMessages()
    setEditingMountId('')
    setMountForm(createMountDraft())
    setMappingFormDialogOpen(true)
  }

  function openEditMappingDialog(mount) {
    resetMessages()
    setEditingMountId(mount.id)
    setMountForm(mountToForm(mount))
    setMappingFormDialogOpen(true)
  }

  function closeMappingFormDialog() {
    setMappingFormDialogOpen(false)
    setEditingMountId('')
    setMountForm(emptyMount)
    if (outputPickerTarget === 'mount') {
      closeOutputDirectoryPicker()
    }
    closeSourceDirectoryPicker()
  }

  async function loadSourceDirectories(path = '', options = {}) {
    const providerID = options.providerID || (sourcePickerTarget === 'partial' ? selectedPartialScanMount?.provider_id : mountForm.provider_id)
    if (!providerID) {
      setSourceDirectoryError('请先选择数据源')
      return
    }

    setSourceDirectoryLoading(true)
    setSourceDirectoryError('')
    try {
      const data = await api.listProviderDirectories(providerID, path, options)
      setSourceDirectoryState(data)
      setSourceDirectoryFilter('')
    } catch (error) {
      setSourceDirectoryError(error.message)
    } finally {
      setSourceDirectoryLoading(false)
    }
  }

  function openSourceDirectoryPicker(target = 'mount', partialPathId = '') {
    const providerID = target === 'partial' ? selectedPartialScanMount?.provider_id : mountForm.provider_id
    const partialPath = partialScanSourcePaths.find((item) => item.id === partialPathId)?.path || ''
    const sourcePath = target === 'partial' ? partialPath : mountForm.source_path
    if (!providerID) {
      setActionError('请先选择数据源')
      return
    }
    setSourcePickerTarget(target)
    setSourcePickerPartialPathId(partialPathId)
    setSourcePickerOpen(true)
    loadSourceDirectories(sourcePath, { providerID })
  }

  function closeSourceDirectoryPicker() {
    setSourcePickerOpen(false)
    setSourcePickerTarget('mount')
    setSourcePickerPartialPathId('')
    setSourceDirectoryError('')
    setSourceDirectoryFilter('')
  }

  function selectSourceDirectory() {
    const selectedPath = sourceDirectoryState?.path
    if (!selectedPath) {
      return
    }
    if (sourcePickerTarget === 'partial') {
      setPartialScanSourcePaths((current) => current.map((item) => (
        item.id === sourcePickerPartialPathId ? { ...item, path: selectedPath, error: '' } : item
      )))
    } else {
      setMountForm((current) => ({ ...current, source_path: selectedPath }))
    }
    closeSourceDirectoryPicker()
  }

  async function loadOutputDirectories(path = '') {
    setOutputDirectoryLoading(true)
    setOutputDirectoryError('')
    try {
      const data = await api.listOutputDirectories(path)
      setOutputDirectoryState(data)
      setNewOutputDirectoryName('')
      setOutputDirectoryFilter('')
    } catch (error) {
      setOutputDirectoryError(error.message)
    } finally {
      setOutputDirectoryLoading(false)
    }
  }

  function openOutputDirectoryPicker(target, path) {
    setOutputPickerTarget(target)
    setOutputPickerOpen(true)
    loadOutputDirectories(path)
  }

  function closeOutputDirectoryPicker() {
    setOutputPickerOpen(false)
    setOutputPickerTarget('')
    setOutputDirectoryError('')
    setNewOutputDirectoryName('')
    setOutputDirectoryFilter('')
  }

  async function handleCreateOutputDirectory(event) {
    event.preventDefault()
    if (!outputDirectoryState?.path || !newOutputDirectoryName.trim()) {
      return
    }

    setOutputDirectoryLoading(true)
    setOutputDirectoryError('')
    try {
      const created = await api.createOutputDirectory(outputDirectoryState.path, newOutputDirectoryName.trim())
      await loadOutputDirectories(created.path)
    } catch (error) {
      setOutputDirectoryError(error.message)
      setOutputDirectoryLoading(false)
    }
  }

  function selectOutputDirectory() {
    const selectedPath = outputDirectoryState?.path
    if (!selectedPath) {
      return
    }
    setMountForm((current) => ({ ...current, target_path: selectedPath }))
    closeOutputDirectoryPicker()
  }

  async function refreshAll() {
    await librariesState.refresh()
    if (selectedLibraryId) {
      await mountsState.refresh()
    }
  }

  async function handleCreateLibrary(event) {
    event.preventDefault()
    resetMessages()
    try {
      await api.createLibrary(libraryForm)
      await librariesState.refresh()
      closeCreateDialog()
      setActionMessage(`媒体库 ${libraryForm.name} 已创建。`)
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleSaveLibrary(event) {
    event.preventDefault()
    resetMessages()
    try {
      await api.updateLibrary(libraryForm.id, libraryForm)
      await librariesState.refresh()
      closeEditDialog()
      setActionMessage(`媒体库 ${libraryForm.name} 已保存。`)
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleDeleteLibrary() {
    if (!libraryForm.id) {
      return
    }
    if (!window.confirm(`删除媒体库 ${libraryForm.id}？关联映射也会被删除。`)) {
      return
    }
    const cleanupOutputs = window.confirm('是否同时删除该媒体库的输出文件？这会删除映射目标路径下生成的 STRM 和下载的附属文件。')
    resetMessages()
    try {
      await api.deleteLibrary(libraryForm.id, { cleanup_outputs: cleanupOutputs })
      setSelectedLibraryId('')
      await librariesState.refresh()
      closeEditDialog()
      closeMappingsDialog()
      setActionMessage(`媒体库 ${libraryForm.id} 已删除${cleanupOutputs ? '，输出文件已清理' : ''}。`)
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleRunLibraryScan(libraryId, payload = {}) {
    resetMessages()
    const scanPayload = { ...payload, overwrite: overwriteScanOutputs }
    try {
      await api.runLibraryScan(libraryId, scanPayload)
      if (scanPayload.target_path) {
        setActionMessage(`${scanPayload.target_path} 的局部扫描已排队${scanPayload.overwrite ? '，会覆盖已有输出' : '，会跳过已有输出'}。`)
      } else {
        setActionMessage(`媒体库 ${libraryId} 的扫描已排队${scanPayload.overwrite ? '，会覆盖已有输出' : '，会跳过已有输出'}。`)
      }
    } catch (error) {
      setActionError(error.message)
    }
  }

  function handlePartialScanMountChange(mountId) {
    const mount = partialScanMounts.find((item) => item.id === mountId)
    setPartialScanMountId(mountId)
    setPartialScanSourcePaths([createPartialScanPath(mount?.source_path || '')])
    setPartialScanSubmittedPaths([])
  }

  function addPartialScanSourcePath() {
    setPartialScanSourcePaths((current) => [...current, createPartialScanPath(selectedPartialScanMount?.source_path || '')])
  }

  function updatePartialScanSourcePath(id, path) {
    setPartialScanSourcePaths((current) => current.map((item) => (
      item.id === id ? { ...item, path, error: '' } : item
    )))
  }

  function removePartialScanSourcePath(id) {
    setPartialScanSourcePaths((current) => {
      const next = current.filter((item) => item.id !== id)
      return next.length > 0 ? next : [createPartialScanPath(selectedPartialScanMount?.source_path || '')]
    })
  }

  async function handleRunPartialScan(event) {
    event.preventDefault()
    resetMessages()
    const sourcePaths = partialScanSourcePaths.map((item) => ({ ...item, path: item.path.trim(), error: '' }))
    const pendingPaths = sourcePaths.filter((item) => item.path)
    if (!partialScanLibrary?.id || !partialScanMountId || pendingPaths.length === 0) {
      setActionError('请选择或输入源目录')
      return
    }

    const failedPaths = []
    const submittedPaths = []
    setPartialScanSubmitting(true)
    try {
      for (const item of pendingPaths) {
        try {
          await api.runLibraryScan(partialScanLibrary.id, { mount_id: partialScanMountId, source_path: item.path, overwrite: overwriteScanOutputs })
          submittedPaths.push({ ...item, submittedAt: new Date().toISOString() })
        } catch (error) {
          failedPaths.push({ ...item, error: error.message })
        }
      }
    } finally {
      setPartialScanSubmitting(false)
    }

    const blankPaths = sourcePaths.filter((item) => !item.path)
    const nextPaths = [...failedPaths, ...blankPaths]
    setPartialScanSourcePaths(nextPaths.length > 0 ? nextPaths : [createPartialScanPath(selectedPartialScanMount?.source_path || '')])
    setPartialScanSubmittedPaths((current) => [...submittedPaths, ...current])
    if (submittedPaths.length > 0) {
      setActionMessage(`${submittedPaths.length} 个源目录已提交成功${overwriteScanOutputs ? '，会覆盖已有输出' : '，会跳过已有输出'}。`)
    }
  }

  async function handleSubmitMount(event) {
    event.preventDefault()
    if (!selectedLibraryId) {
      return
    }
    resetMessages()
    try {
      if (editingMountId) {
        await api.updateMount(selectedLibraryId, editingMountId, mountForm)
        setActionMessage(`映射 ${editingMountId} 已更新。`)
      } else {
        await api.createMount(selectedLibraryId, mountForm)
        setActionMessage(`映射 ${mountForm.id} 已创建。`)
      }
      await refreshAll()
      closeMappingFormDialog()
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleDeleteMount(mount) {
    if (!window.confirm(`删除映射 ${mount.id}？`)) {
      return
    }
    const cleanupOutputs = window.confirm(`是否同时删除目标路径 ${mount.target_path} 下的输出文件？`)
    resetMessages()
    try {
      await api.deleteMount(selectedLibraryId, mount.id, { cleanup_outputs: cleanupOutputs })
      await refreshAll()
      setActionMessage(`映射 ${mount.id} 已删除${cleanupOutputs ? '，输出文件已清理' : ''}。`)
      if (editingMountId === mount.id) {
        closeMappingFormDialog()
      }
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleDropMount(targetMountId) {
    if (!selectedLibraryId || !draggedMountId || draggedMountId === targetMountId) {
      setDraggedMountId('')
      setDropTargetMountId('')
      return
    }

    resetMessages()
    try {
      const ordered = reorderItems(mountsState.data || [], draggedMountId, targetMountId)
      for (let index = 0; index < ordered.length; index += 1) {
        const mount = ordered[index]
        await api.updateMount(selectedLibraryId, mount.id, mountToPayload(mount, { priority: (index + 1) * 100 }))
      }
      await refreshAll()
      setActionMessage('映射顺序已保存。')
    } catch (error) {
      setActionError(error.message)
    } finally {
      setDraggedMountId('')
      setDropTargetMountId('')
    }
  }

  const sourceDirectoryItems = sourceDirectoryState?.items || []
  const filteredSourceDirectoryItems = filterDirectoryItems(sourceDirectoryItems, sourceDirectoryFilter)
  const outputDirectoryItems = outputDirectoryState?.items || []
  const filteredOutputDirectoryItems = filterDirectoryItems(outputDirectoryItems, outputDirectoryFilter)

  return (
    <div className="page-grid one-col">
      <PageSection
          title="媒体库"
        actions={(
          <>
            <button type="button" className="ghost-button" onClick={librariesState.refresh}>刷新</button>
            <label className="check-inline"><input type="checkbox" checked={overwriteScanOutputs} onChange={(e) => setOverwriteScanOutputs(e.target.checked)} /> 覆盖已有输出</label>
            <button type="button" onClick={openCreateDialog}>添加媒体库</button>
          </>
        )}
      >
        <StatusBanner error={librariesState.error || actionError} loading={librariesState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>媒体库</th>
                  <th>映射</th>
                  <th>启用</th>
                  <th>上次扫描</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {(librariesState.data || []).map((library) => (
                  <tr key={library.id}>
                    <td>
                      <div>{library.name}</div>
                      <div className="subtle-id">{library.id}</div>
                    </td>
                    <td>共 {library.mountCount} 个 / 启用 {library.enabledMountCount} 个</td>
                    <td>{library.enabled ? '是' : '否'}</td>
                    <td>{formatLocalDateTime(library.last_scan_at, systemTimeZone)}</td>
                    <td>
                      <div className="button-row">
                        <button type="button" className="ghost-button" onClick={() => handleRunLibraryScan(library.id)}>扫描整个库</button>
                        <button type="button" className="ghost-button" onClick={() => openEditDialog(library)}>编辑媒体库</button>
                        <button type="button" className="ghost-button" onClick={() => openPartialScanDialog(library)}>局部扫描</button>
                        <button type="button" onClick={() => openMappingsDialog(library)}>管理映射</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {(librariesState.data || []).length === 0 ? (
                  <tr><td colSpan="5" className="empty-cell">暂无媒体库。</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
          {actionMessage ? <div className="hint top-gap">{actionMessage}</div> : null}
        </StatusBanner>
      </PageSection>

      {createDialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeCreateDialog}>
          <div className="modal-card library-modal-card" role="dialog" aria-modal="true" aria-labelledby="library-create-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="library-create-dialog-title">添加媒体库</h2>
                <p>先创建媒体库，UUID 会自动生成。</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeCreateDialog}>关闭</button>
            </div>
            <form className="form-grid" onSubmit={handleCreateLibrary}>
              <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="媒体库名称" required />
              <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="描述" />
              <input value={libraryForm.scan_cron} onChange={(e) => setLibraryForm({ ...libraryForm, scan_cron: e.target.value })} placeholder="扫描 cron，例如 0 4 * * *" />
              <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> 启用</label>
              <div className="button-row">
                <button type="submit">创建媒体库</button>
              </div>
            </form>
            {actionError ? <div className="hint top-gap">{actionError}</div> : null}
          </div>
        </div>
      ) : null}

      {editDialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeEditDialog}>
          <div className="modal-card library-modal-card" role="dialog" aria-modal="true" aria-labelledby="library-edit-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="library-edit-dialog-title">编辑媒体库</h2>
                <p>{libraryForm.name ? `编辑 ${libraryForm.name} 的设置。` : '编辑媒体库设置。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeEditDialog}>关闭</button>
            </div>
            <form className="form-grid" onSubmit={handleSaveLibrary}>
              <input value={libraryForm.id} disabled placeholder="媒体库 ID" />
              <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="媒体库名称" required />
              <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="描述" />
              <input value={libraryForm.scan_cron} onChange={(e) => setLibraryForm({ ...libraryForm, scan_cron: e.target.value })} placeholder="扫描 cron，例如 0 4 * * *" />
              <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> 启用</label>
              <div className="button-row">
                <button type="submit">保存媒体库</button>
                <button type="button" className="danger" onClick={handleDeleteLibrary}>删除媒体库</button>
              </div>
            </form>
            {actionError ? <div className="hint top-gap">{actionError}</div> : null}
          </div>
        </div>
      ) : null}

      {partialScanDialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closePartialScanDialog}>
          <div className="modal-card library-modal-card" role="dialog" aria-modal="true" aria-labelledby="partial-scan-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="partial-scan-dialog-title">局部扫描</h2>
                <p>{partialScanLibrary ? `选择 ${partialScanLibrary.name} 的数据源目录进行递归扫描。` : '选择数据源目录进行递归扫描。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closePartialScanDialog}>关闭</button>
            </div>
            <form className="form-grid top-gap" onSubmit={handleRunPartialScan}>
              <select value={partialScanMountId} onChange={(event) => handlePartialScanMountChange(event.target.value)} disabled={partialScanMountsLoading || partialScanMounts.length === 0} required>
                <option value="">选择启用映射</option>
                {partialScanMounts.map((mount) => (
                  <option key={mount.id} value={mount.id}>{mount.provider_id}: {mount.source_path} → {mount.target_path}</option>
                ))}
              </select>
              <div className="partial-scan-path-list">
                {partialScanSourcePaths.map((item) => (
                  <div className="partial-scan-path-item" key={item.id}>
                    <div className="path-input-row">
                      <input value={item.path} onChange={(event) => updatePartialScanSourcePath(item.id, event.target.value)} placeholder="要扫描的源目录，例如 /Video/TV/Anime" />
                      <button type="button" className="ghost-button" onClick={() => openSourceDirectoryPicker('partial', item.id)} disabled={!selectedPartialScanMount || partialScanSubmitting}>浏览</button>
                    </div>
                    <div className="button-row">
                      <button type="button" className="ghost-button" onClick={() => removePartialScanSourcePath(item.id)} disabled={partialScanSubmitting || (partialScanSourcePaths.length === 1 && !item.path.trim())}>移除此项</button>
                    </div>
                    {item.error ? <div className="banner banner-error">{item.path}：{item.error}</div> : null}
                  </div>
                ))}
                <div className="button-row">
                  <button type="button" className="ghost-button" onClick={addPartialScanSourcePath} disabled={partialScanSubmitting}>添加源目录</button>
                </div>
              </div>
              <label className="check-inline"><input type="checkbox" checked={overwriteScanOutputs} onChange={(event) => setOverwriteScanOutputs(event.target.checked)} /> 覆盖已有输出</label>
              <div className="hint">源目录必须位于所选映射的来源路径下。可以添加多个源目录，提交时会逐条排队，失败项会保留在表单中。</div>
              <div className="button-row">
                <button type="submit" disabled={partialScanMountsLoading || partialScanSubmitting || !selectedPartialScanMount}>{partialScanSubmitting ? '正在提交...' : '开始局部扫描'}</button>
              </div>
            </form>
            {partialScanSubmittedPaths.length > 0 ? (
              <div className="partial-scan-submitted top-gap">
                <h3>已提交成功</h3>
                <div className="directory-list compact-list">
                  {partialScanSubmittedPaths.map((item) => (
                    <div className="directory-item success-item" key={`${item.id}-${item.submittedAt}`}>
                      <span>{item.path}</span>
                      <code>已排队</code>
                    </div>
                  ))}
                </div>
              </div>
            ) : null}
            {partialScanMountsLoading ? <div className="hint top-gap">正在读取映射...</div> : null}
            {actionError ? <div className="hint top-gap">{actionError}</div> : null}
          </div>
        </div>
      ) : null}

      {mappingsDialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeMappingsDialog}>
          <div className="modal-card mappings-modal-card" role="dialog" aria-modal="true" aria-labelledby="library-mappings-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="library-mappings-dialog-title">管理映射</h2>
                <p>{selectedLibrary ? `${selectedLibrary.name} 的映射。` : '管理该媒体库的来源映射。'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeMappingsDialog}>关闭</button>
            </div>

            <div className="section-heading">
              <div>
                <h3>映射列表</h3>
                <div className="hint">映射定义数据源、网盘完整来源路径和 STRM 目标路径。</div>
              </div>
              <div className="button-row">
                <button type="button" className="ghost-button" onClick={mountsState.refresh}>刷新</button>
                <button type="button" onClick={openCreateMappingDialog}>添加映射</button>
              </div>
            </div>

            <StatusBanner error={mountsState.error || providersState.error} loading={mountsState.loading || providersState.loading}>
              <div className="table-wrap top-gap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>映射</th>
                      <th>数据源</th>
                      <th>来源路径</th>
                      <th>目标路径</th>
                      <th>启用</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(mountsState.data || []).map((mount) => (
                      <tr
                        key={mount.id}
                        draggable
                        className={dropTargetMountId === mount.id ? 'row-selected row-drag-target' : ''}
                        onDragStart={() => {
                          setDraggedMountId(mount.id)
                          setDropTargetMountId(mount.id)
                        }}
                        onDragOver={(event) => {
                          event.preventDefault()
                          if (dropTargetMountId !== mount.id) {
                            setDropTargetMountId(mount.id)
                          }
                        }}
                        onDragEnd={() => {
                          setDraggedMountId('')
                          setDropTargetMountId('')
                        }}
                        onDrop={(event) => {
                          event.preventDefault()
                          handleDropMount(mount.id)
                        }}
                      >
                        <td>{mount.id}</td>
                        <td>{mount.provider_id}</td>
                        <td className="mono-text">{mount.source_path}</td>
                        <td className="mono-text">{mount.target_path}</td>
                        <td>{mount.enabled ? '是' : '否'}</td>
                        <td>
                          <div className="button-row">
                            <button type="button" className="ghost-button" onClick={() => openEditMappingDialog(mount)}>编辑</button>
                            <button type="button" className="danger" onClick={() => handleDeleteMount(mount)}>删除</button>
                          </div>
                        </td>
                      </tr>
                    ))}
                    {(mountsState.data || []).length === 0 ? (
                      <tr><td colSpan="6" className="empty-cell">暂无映射。</td></tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
            </StatusBanner>

            {actionMessage ? <div className="hint top-gap">{actionMessage}</div> : null}
            {actionError ? <div className="hint top-gap">{actionError}</div> : null}

            {mappingFormDialogOpen ? (
              <div className="modal-backdrop" role="presentation" onClick={closeMappingFormDialog}>
                <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="mapping-form-dialog-title" onClick={(event) => event.stopPropagation()}>
                  <div className="modal-header">
                    <div>
                      <h2 id="mapping-form-dialog-title">{editingMountId ? '编辑映射' : '添加映射'}</h2>
                      <p>{selectedLibrary ? `管理 ${selectedLibrary.name} 的映射。` : '管理映射。'}</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={closeMappingFormDialog}>关闭</button>
                  </div>
                  <form className="form-grid" onSubmit={handleSubmitMount}>
                    {editingMountId ? <input value={mountForm.id} disabled placeholder="映射 ID" /> : null}
                    <select value={mountForm.provider_id} onChange={(e) => setMountForm({ ...mountForm, provider_id: e.target.value })} required>
                      <option value="">选择数据源</option>
                      {(providersState.data || []).map((provider) => (
                      <option key={provider.id} value={provider.id}>{provider.name} ({provider.id})</option>
                      ))}
                    </select>
                    <div className="path-input-row">
                    <input value={mountForm.source_path} onChange={(e) => setMountForm({ ...mountForm, source_path: e.target.value })} placeholder="网盘完整来源路径，例如 /Video/TV/Anime" required />
                      <button type="button" className="ghost-button" onClick={() => openSourceDirectoryPicker('mount')} disabled={!mountForm.provider_id}>浏览</button>
                    </div>
                    <div className="path-input-row">
                      <input value={mountForm.target_path} onChange={(e) => setMountForm({ ...mountForm, target_path: e.target.value })} placeholder="目标路径" required />
                      <button type="button" className="ghost-button" onClick={() => openOutputDirectoryPicker('mount', mountForm.target_path)}>浏览</button>
                    </div>
                    <label className="check-inline"><input type="checkbox" checked={mountForm.enabled} onChange={(e) => setMountForm({ ...mountForm, enabled: e.target.checked })} /> 启用</label>
                    <div className="button-row">
                      <button type="submit">{editingMountId ? '保存映射' : '创建映射'}</button>
                    </div>
                  </form>
                  {actionError ? <div className="hint top-gap">{actionError}</div> : null}
                </div>
              </div>
            ) : null}

            {outputPickerOpen ? (
              <div className="modal-backdrop nested-modal" role="presentation" onClick={closeOutputDirectoryPicker}>
                <div className="modal-card directory-picker-card" role="dialog" aria-modal="true" aria-labelledby="output-directory-picker-title" onClick={(event) => event.stopPropagation()}>
                  <div className="modal-header">
                    <div>
                      <h2 id="output-directory-picker-title">选择目标目录</h2>
                      <p>浏览 STRM 输出根目录，选择后会回填为目标路径。</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={closeOutputDirectoryPicker}>关闭</button>
                  </div>

                  <div className="directory-toolbar top-gap">
                    <button type="button" className="ghost-button" onClick={() => loadOutputDirectories('/')}>输出根目录</button>
                    <button type="button" className="ghost-button" onClick={() => loadOutputDirectories(outputDirectoryState?.parent_path)} disabled={!outputDirectoryState?.parent_path || outputDirectoryLoading}>上级目录</button>
                    <button type="button" className="ghost-button" onClick={() => loadOutputDirectories(outputDirectoryState?.path)} disabled={!outputDirectoryState?.path || outputDirectoryLoading}>刷新</button>
                  </div>

                  <div className="directory-current mono-text top-gap">
                    {outputDirectoryState?.path || '正在加载...'}
                    {outputDirectoryState?.output_root ? <span className="directory-root-hint">输出根目录：{outputDirectoryState.output_root}</span> : null}
                  </div>

                  <form className="directory-toolbar top-gap" onSubmit={handleCreateOutputDirectory}>
                    <input value={newOutputDirectoryName} onChange={(event) => setNewOutputDirectoryName(event.target.value)} placeholder="新建目标目录名称" />
                    <button type="submit" disabled={!outputDirectoryState?.path || outputDirectoryLoading}>新建目录</button>
                  </form>

                  {outputDirectoryError ? <div className="banner banner-error top-gap">{outputDirectoryError}</div> : null}
                  {outputDirectoryLoading ? <div className="hint top-gap">正在读取目录...</div> : null}

                  <div className="directory-filter top-gap">
                    <input value={outputDirectoryFilter} onChange={(event) => setOutputDirectoryFilter(event.target.value)} placeholder="搜索当前目标目录下的子目录" />
                  </div>

                  <div className="directory-list top-gap">
                    {filteredOutputDirectoryItems.map((item) => (
                      <button type="button" className="directory-item" key={item.path} onClick={() => loadOutputDirectories(item.path)}>
                        <span>{item.name}</span>
                        <code>{item.path}</code>
                      </button>
                    ))}
                    {!outputDirectoryLoading && outputDirectoryItems.length === 0 ? <div className="empty-cell">当前目标目录下没有子目录。</div> : null}
                    {!outputDirectoryLoading && outputDirectoryItems.length > 0 && filteredOutputDirectoryItems.length === 0 ? <div className="empty-cell">没有匹配的子目录。</div> : null}
                  </div>

                  <div className="button-row top-gap">
                    <button type="button" onClick={selectOutputDirectory} disabled={!outputDirectoryState?.path}>选择当前目录</button>
                  </div>
                </div>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}

      {sourcePickerOpen ? (
        <div className="modal-backdrop nested-modal" role="presentation" onClick={closeSourceDirectoryPicker}>
          <div className="modal-card directory-picker-card" role="dialog" aria-modal="true" aria-labelledby="source-directory-picker-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="source-directory-picker-title">选择源目录</h2>
                <p>浏览当前数据源的目录，选择后回填来源路径。</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeSourceDirectoryPicker}>关闭</button>
            </div>

            <div className="directory-toolbar top-gap">
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories('/')}>源根目录</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.parent_path)} disabled={!sourceDirectoryState?.parent_path || sourceDirectoryLoading}>上级目录</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.path)} disabled={!sourceDirectoryState?.path || sourceDirectoryLoading}>刷新</button>
              <button type="button" className="ghost-button" onClick={() => loadSourceDirectories(sourceDirectoryState?.path, { force: true })} disabled={!sourceDirectoryState?.path || sourceDirectoryLoading}>强制刷新</button>
            </div>

            <div className="directory-current mono-text top-gap">
              {sourceDirectoryState?.path || '正在加载...'}
              {sourceDirectoryState?.provider_id ? <span className="directory-root-hint">数据源：{sourceDirectoryState.provider_id}</span> : null}
            </div>

            {sourceDirectoryError ? <div className="banner banner-error top-gap">{sourceDirectoryError}</div> : null}
            {sourceDirectoryLoading ? <div className="hint top-gap">正在读取目录...</div> : null}

            <div className="directory-filter top-gap">
              <input value={sourceDirectoryFilter} onChange={(event) => setSourceDirectoryFilter(event.target.value)} placeholder="搜索当前源目录下的子目录" />
            </div>

            <div className="directory-list top-gap">
              {filteredSourceDirectoryItems.map((item) => (
                <button type="button" className="directory-item" key={item.path} onClick={() => loadSourceDirectories(item.path)}>
                  <span>{item.name}</span>
                  <code>{item.path}</code>
                </button>
              ))}
              {!sourceDirectoryLoading && sourceDirectoryItems.length === 0 ? <div className="empty-cell">当前源目录下没有子目录。</div> : null}
              {!sourceDirectoryLoading && sourceDirectoryItems.length > 0 && filteredSourceDirectoryItems.length === 0 ? <div className="empty-cell">没有匹配的子目录。</div> : null}
            </div>

            <div className="button-row top-gap">
              <button type="button" onClick={selectSourceDirectory} disabled={!sourceDirectoryState?.path}>选择当前目录</button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}
