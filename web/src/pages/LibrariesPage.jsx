import { useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const emptyLibrary = { id: '', name: '', description: '', enabled: true }
const emptyMount = { libraryId: '', id: '', provider_id: '', source_path: '', target_path: '', media_type: '', priority: 100, enabled: true }

export function LibrariesPage() {
  const [libraryForm, setLibraryForm] = useState(emptyLibrary)
  const [mountForm, setMountForm] = useState(emptyMount)
  const librariesState = useAsyncData(async () => (await api.listLibraries()).items || [], [])
  const mountsState = useAsyncData(async () => {
    if (!mountForm.libraryId) return []
    return (await api.listMounts(mountForm.libraryId)).items || []
  }, [mountForm.libraryId])

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

  return (
    <div className="page-grid two-col">
      <PageSection title="Create Library">
        <form className="form-grid" onSubmit={handleCreateLibrary}>
          <input value={libraryForm.id} onChange={(e) => setLibraryForm({ ...libraryForm, id: e.target.value })} placeholder="id" required />
          <input value={libraryForm.name} onChange={(e) => setLibraryForm({ ...libraryForm, name: e.target.value })} placeholder="name" required />
          <input value={libraryForm.description} onChange={(e) => setLibraryForm({ ...libraryForm, description: e.target.value })} placeholder="description" />
          <label className="check-inline"><input type="checkbox" checked={libraryForm.enabled} onChange={(e) => setLibraryForm({ ...libraryForm, enabled: e.target.checked })} /> enabled</label>
          <button type="submit">Create</button>
        </form>
      </PageSection>
      <PageSection title="Libraries" actions={<button onClick={librariesState.refresh}>Refresh</button>}>
        <StatusBanner error={librariesState.error} loading={librariesState.loading}>
          <JsonBlock value={librariesState.data} />
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
          <JsonBlock value={mountsState.data} />
        </StatusBanner>
      </PageSection>
    </div>
  )
}
