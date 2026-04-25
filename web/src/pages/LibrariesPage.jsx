import { useMemo, useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const emptyLibrary = { id: '', name: '', description: '', scan_cron: '', enabled: true }
const emptyMount = { id: '', provider_id: '', source_path: '', target_path: '', media_type: '', priority: 100, enabled: true }

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
  const [createDialogOpen, setCreateDialogOpen] = useState(false)
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [mappingsDialogOpen, setMappingsDialogOpen] = useState(false)
  const [mappingFormDialogOpen, setMappingFormDialogOpen] = useState(false)
  const [selectedLibraryId, setSelectedLibraryId] = useState('')
  const [libraryForm, setLibraryForm] = useState(emptyLibrary)
  const [mountForm, setMountForm] = useState(emptyMount)
  const [editingMountId, setEditingMountId] = useState('')
  const [draggedMountId, setDraggedMountId] = useState('')
  const [dropTargetMountId, setDropTargetMountId] = useState('')
  const [partialScanTargetPath, setPartialScanTargetPath] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')

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
    setPartialScanTargetPath('')
    setMountForm(emptyMount)
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
      setActionMessage(`Library ${libraryForm.name} created.`)
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
      setActionMessage(`Library ${libraryForm.name} saved.`)
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleDeleteLibrary() {
    if (!libraryForm.id) {
      return
    }
    if (!window.confirm(`Delete library ${libraryForm.id}? Mappings will also be deleted.`)) {
      return
    }
    const cleanupOutputs = window.confirm('Also delete output files for this library? This removes generated STRM and downloaded sidecar files under its mapping target paths.')
    resetMessages()
    try {
      await api.deleteLibrary(libraryForm.id, { cleanup_outputs: cleanupOutputs })
      setSelectedLibraryId('')
      await librariesState.refresh()
      closeEditDialog()
      closeMappingsDialog()
      setActionMessage(`Library ${libraryForm.id} deleted${cleanupOutputs ? ' and output files cleaned' : ''}.`)
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleRunLibraryScan(libraryId, payload = {}) {
    resetMessages()
    try {
      await api.runLibraryScan(libraryId, payload)
      if (payload.target_path) {
        setActionMessage(`Partial scan queued for ${payload.target_path}.`)
      } else {
        setActionMessage(`Library scan queued for ${libraryId}.`)
      }
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleRunPartialScan(event) {
    event.preventDefault()
    resetMessages()
    const targetPath = partialScanTargetPath.trim()
    if (!selectedLibraryId || !targetPath) {
      setActionError('target_path is required')
      return
    }
    await handleRunLibraryScan(selectedLibraryId, { target_path: targetPath })
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
        setActionMessage(`Mapping ${editingMountId} updated.`)
      } else {
        await api.createMount(selectedLibraryId, mountForm)
        setActionMessage(`Mapping ${mountForm.id} created.`)
      }
      await refreshAll()
      closeMappingFormDialog()
    } catch (error) {
      setActionError(error.message)
    }
  }

  async function handleDeleteMount(mount) {
    if (!window.confirm(`Delete mapping ${mount.id}?`)) {
      return
    }
    const cleanupOutputs = window.confirm(`Also delete output files under target path ${mount.target_path}?`)
    resetMessages()
    try {
      await api.deleteMount(selectedLibraryId, mount.id, { cleanup_outputs: cleanupOutputs })
      await refreshAll()
      setActionMessage(`Mapping ${mount.id} deleted${cleanupOutputs ? ' and output files cleaned' : ''}.`)
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
      setActionMessage('Mapping order saved.')
    } catch (error) {
      setActionError(error.message)
    } finally {
      setDraggedMountId('')
      setDropTargetMountId('')
    }
  }

  return (
    <div className="page-grid one-col">
      <PageSection
        title="Libraries"
        actions={(
          <>
            <button type="button" className="ghost-button" onClick={librariesState.refresh}>Refresh</button>
            <button type="button" onClick={openCreateDialog}>Add Library</button>
          </>
        )}
      >
        <StatusBanner error={librariesState.error || actionError} loading={librariesState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Library</th>
                  <th>Mappings</th>
                  <th>Enabled</th>
                  <th>Last Scan</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {(librariesState.data || []).map((library) => (
                  <tr key={library.id}>
                    <td>
                      <div>{library.name}</div>
                      <div className="subtle-id">{library.id}</div>
                    </td>
                    <td>{library.mountCount} total / {library.enabledMountCount} enabled</td>
                    <td>{library.enabled ? 'Yes' : 'No'}</td>
                    <td>{formatLocalDateTime(library.last_scan_at)}</td>
                    <td>
                      <div className="button-row">
                        <button type="button" className="ghost-button" onClick={() => handleRunLibraryScan(library.id)}>Scan</button>
                        <button type="button" className="ghost-button" onClick={() => openEditDialog(library)}>Edit Library</button>
                        <button type="button" onClick={() => openMappingsDialog(library)}>Manage Mappings</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {(librariesState.data || []).length === 0 ? (
                  <tr><td colSpan="5" className="empty-cell">No libraries found.</td></tr>
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
                <h2 id="library-create-dialog-title">Add Library</h2>
                <p>Create the library first. A UUID will be generated automatically.</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeCreateDialog}>Close</button>
            </div>
            <form className="form-grid" onSubmit={handleCreateLibrary}>
              <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="library name" required />
              <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="description" />
              <input value={libraryForm.scan_cron} onChange={(e) => setLibraryForm({ ...libraryForm, scan_cron: e.target.value })} placeholder="scan cron, e.g. 0 4 * * *" />
              <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> enabled</label>
              <div className="button-row">
                <button type="submit">Create Library</button>
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
                <h2 id="library-edit-dialog-title">Edit Library</h2>
                <p>{libraryForm.name ? `Edit settings for ${libraryForm.name}.` : 'Edit library settings.'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeEditDialog}>Close</button>
            </div>
            <form className="form-grid" onSubmit={handleSaveLibrary}>
              <input value={libraryForm.id} disabled placeholder="library id" />
              <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="library name" required />
              <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="description" />
              <input value={libraryForm.scan_cron} onChange={(e) => setLibraryForm({ ...libraryForm, scan_cron: e.target.value })} placeholder="scan cron, e.g. 0 4 * * *" />
              <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> enabled</label>
              <div className="button-row">
                <button type="submit">Save Library</button>
                <button type="button" className="danger" onClick={handleDeleteLibrary}>Delete Library</button>
              </div>
            </form>
            {actionError ? <div className="hint top-gap">{actionError}</div> : null}
          </div>
        </div>
      ) : null}

      {mappingsDialogOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={closeMappingsDialog}>
          <div className="modal-card mappings-modal-card" role="dialog" aria-modal="true" aria-labelledby="library-mappings-dialog-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="library-mappings-dialog-title">Manage Mappings</h2>
                <p>{selectedLibrary ? `Mappings for ${selectedLibrary.name}.` : 'Manage source mappings for this library.'}</p>
              </div>
              <button type="button" className="ghost-button" onClick={closeMappingsDialog}>Close</button>
            </div>

            <div className="section-heading">
              <div>
                <h3>Mapping List</h3>
                <div className="hint">Mappings define provider, source path, and STRM target path.</div>
              </div>
              <div className="button-row">
                <button type="button" className="ghost-button" onClick={mountsState.refresh}>Refresh</button>
                <button type="button" onClick={openCreateMappingDialog}>Add Mapping</button>
              </div>
            </div>

            <StatusBanner error={mountsState.error || providersState.error} loading={mountsState.loading || providersState.loading}>
              <form className="form-grid compact top-gap" onSubmit={handleRunPartialScan}>
                <input value={partialScanTargetPath} onChange={(e) => setPartialScanTargetPath(e.target.value)} placeholder="target path to scan, e.g. /Anime/Dragon Raja" />
                <div className="button-row">
                  <button type="submit" className="ghost-button">Partial Scan</button>
                </div>
                <div className="hint">Use the STRM target path. The server will map it back to the matching source path.</div>
              </form>
              <div className="table-wrap top-gap">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Mapping</th>
                      <th>Provider</th>
                      <th>Source</th>
                      <th>Target</th>
                      <th>Enabled</th>
                      <th>Actions</th>
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
                        <td>{mount.enabled ? 'Yes' : 'No'}</td>
                        <td>
                          <div className="button-row">
                            <button type="button" className="ghost-button" onClick={() => openEditMappingDialog(mount)}>Edit</button>
                            <button type="button" className="danger" onClick={() => handleDeleteMount(mount)}>Delete</button>
                          </div>
                        </td>
                      </tr>
                    ))}
                    {(mountsState.data || []).length === 0 ? (
                      <tr><td colSpan="6" className="empty-cell">No mappings found.</td></tr>
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
                      <h2 id="mapping-form-dialog-title">{editingMountId ? 'Edit Mapping' : 'Add Mapping'}</h2>
                      <p>{selectedLibrary ? `Manage mapping for ${selectedLibrary.name}.` : 'Manage mapping.'}</p>
                    </div>
                    <button type="button" className="ghost-button" onClick={closeMappingFormDialog}>Close</button>
                  </div>
                  <form className="form-grid" onSubmit={handleSubmitMount}>
                    {editingMountId ? <input value={mountForm.id} disabled placeholder="mapping id" /> : null}
                    <select value={mountForm.provider_id} onChange={(e) => setMountForm({ ...mountForm, provider_id: e.target.value })} required>
                      <option value="">select provider</option>
                      {(providersState.data || []).map((provider) => (
                        <option key={provider.id} value={provider.id}>{provider.name} ({provider.id})</option>
                      ))}
                    </select>
                    <input value={mountForm.source_path} onChange={(e) => setMountForm({ ...mountForm, source_path: e.target.value })} placeholder="source path" required />
                    <input value={mountForm.target_path} onChange={(e) => setMountForm({ ...mountForm, target_path: e.target.value })} placeholder="target path" required />
                    <label className="check-inline"><input type="checkbox" checked={mountForm.enabled} onChange={(e) => setMountForm({ ...mountForm, enabled: e.target.checked })} /> enabled</label>
                    <div className="button-row">
                      <button type="submit">{editingMountId ? 'Save Mapping' : 'Create Mapping'}</button>
                    </div>
                  </form>
                  {actionError ? <div className="hint top-gap">{actionError}</div> : null}
                </div>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  )
}
