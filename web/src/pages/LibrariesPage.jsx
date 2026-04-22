import { useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyLibrary = { id: '', name: '', description: '', enabled: true }
const emptyMount = { libraryId: '', id: '', provider_id: '', source_path: '', target_path: '', media_type: '', priority: 100, enabled: true }

export function LibrariesPage() {
  const [libraryForm, setLibraryForm] = useState(emptyLibrary)
  const [mountForm, setMountForm] = useState(emptyMount)
  const [selectedLibraryId, setSelectedLibraryId] = useState('')
  const librariesState = useAsyncData(async () => (await api.listLibraries()).items || [], [])
  const mountsState = useAsyncData(async () => {
    const libraryId = mountForm.libraryId || selectedLibraryId
    if (!libraryId) return []
    return (await api.listMounts(libraryId)).items || []
  }, [mountForm.libraryId, selectedLibraryId])

  async function handleCreateLibrary(event) {
    event.preventDefault()
    await api.createLibrary(libraryForm)
    setLibraryForm(emptyLibrary)
    librariesState.refresh()
  }

  async function handleCreateMount(event) {
    event.preventDefault()
    const { libraryId, ...payload } = mountForm
    await api.createMount(libraryId, payload)
    mountsState.refresh()
  }

  async function handleDeleteMount() {
    await api.deleteMount(mountForm.libraryId, mountForm.id)
    mountsState.refresh()
  }

  async function handleDeleteLibrary() {
    if (!libraryForm.id) {
      return
    }
    await api.deleteLibrary(libraryForm.id)
    librariesState.refresh()
  }

  async function handleRunLibraryScan() {
    const libraryId = selectedLibraryId || mountForm.libraryId || libraryForm.id
    if (!libraryId) {
      return
    }
    await api.runLibraryScan(libraryId)
  }

  function handleSelectLibrary(library) {
    setSelectedLibraryId(library.id)
    setLibraryForm({
      id: library.id,
      name: library.name,
      description: library.description || '',
      enabled: library.enabled,
    })
    setMountForm((current) => ({ ...current, libraryId: library.id }))
  }

  return (
    <div className="page-grid two-col">
      <PageSection title="Create Library">
        <form className="form-grid" onSubmit={handleCreateLibrary}>
          <input value={libraryForm.id} onChange={(e) => setLibraryForm({ ...libraryForm, id: e.target.value })} placeholder="id" required />
          <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="name" required />
          <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="description" />
          <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> enabled</label>
          <div className="button-row">
            <button type="submit">Create</button>
            <button type="button" className="danger" onClick={handleDeleteLibrary}>Delete</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Libraries" actions={<button onClick={librariesState.refresh}>Refresh</button>}>
        <StatusBanner error={librariesState.error} loading={librariesState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Name</th>
                  <th>Enabled</th>
                  <th>Last Scan</th>
                </tr>
              </thead>
              <tbody>
                {(librariesState.data || []).map((library) => (
                  <tr
                    key={library.id}
                    className={selectedLibraryId === library.id ? 'row-selected' : ''}
                    onClick={() => handleSelectLibrary(library)}
                  >
                    <td>{library.id}</td>
                    <td>{library.name}</td>
                    <td>{String(library.enabled)}</td>
                    <td>{library.last_scan_at || '-'}</td>
                  </tr>
                ))}
                {(librariesState.data || []).length === 0 ? (
                  <tr><td colSpan="4" className="empty-cell">No libraries found.</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
          <div className="button-row top-gap">
            <button onClick={handleRunLibraryScan} disabled={!(selectedLibraryId || mountForm.libraryId || libraryForm.id)}>Run Selected Library Scan</button>
          </div>
        </StatusBanner>
      </PageSection>
      <PageSection title="Mounts">
        <form className="form-grid" onSubmit={handleCreateMount}>
          <input value={mountForm.libraryId} onChange={(e) => setMountForm({ ...mountForm, libraryId: e.target.value })} placeholder="library id" required />
          <input value={mountForm.id} onChange={(e) => setMountForm({ ...mountForm, id: e.target.value })} placeholder="mount id" required />
          <input value={mountForm.provider_id} onChange={(e) => setMountForm({ ...mountForm, provider_id: e.target.value })} placeholder="provider id" required />
          <input value={mountForm.source_path} onChange={(e) => setMountForm({ ...mountForm, source_path: e.target.value })} placeholder="source path" required />
          <input value={mountForm.target_path} onChange={(e) => setMountForm({ ...mountForm, target_path: e.target.value })} placeholder="target path" required />
          <input value={mountForm.media_type} onChange={(e) => setMountForm({ ...mountForm, media_type: e.target.value })} placeholder="media type" />
          <input type="number" value={mountForm.priority} onChange={(e) => setMountForm({ ...mountForm, priority: Number(e.target.value || '100') })} min="1" />
          <label className="check-inline"><input type="checkbox" checked={mountForm.enabled} onChange={(e) => setMountForm({ ...mountForm, enabled: e.target.checked })} /> enabled</label>
          <div className="button-row">
            <button type="submit">Create</button>
            <button type="button" onClick={mountsState.refresh}>Load</button>
            <button type="button" className="danger" onClick={handleDeleteMount}>Delete</button>
          </div>
        </form>
      </PageSection>
      <PageSection title="Mount List">
        <StatusBanner error={mountsState.error} loading={mountsState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Provider</th>
                  <th>Source</th>
                  <th>Target</th>
                  <th>Priority</th>
                </tr>
              </thead>
              <tbody>
                {(mountsState.data || []).map((mount) => (
                  <tr key={mount.id} onClick={() => setMountForm({
                    libraryId: mount.library_id,
                    id: mount.id,
                    provider_id: mount.provider_id,
                    source_path: mount.source_path,
                    target_path: mount.target_path,
                    media_type: mount.media_type || '',
                    priority: mount.priority,
                    enabled: mount.enabled,
                  })}>
                    <td>{mount.id}</td>
                    <td>{mount.provider_id}</td>
                    <td>{mount.source_path}</td>
                    <td>{mount.target_path}</td>
                    <td>{mount.priority}</td>
                  </tr>
                ))}
                {(mountsState.data || []).length === 0 ? (
                  <tr><td colSpan="5" className="empty-cell">No mounts found.</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>
    </div>
  )
}
