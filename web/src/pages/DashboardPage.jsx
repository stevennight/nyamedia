import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

function InfoField({ label, value, mono = false }) {
  return (
    <div className="info-field">
      <span>{label}</span>
      <strong className={mono ? 'mono-text' : undefined}>{value || 'Not set'}</strong>
    </div>
  )
}

export function DashboardPage() {
  const state = useAsyncData(async () => {
    const [systemInfo, providers, libraries, tasks] = await Promise.all([
      api.systemInfo(),
      api.listProviders(),
      api.listLibraries(),
      api.listTasks(),
    ])
    return {
      systemInfo,
      providers: providers.items || [],
      libraries: libraries.items || [],
      tasks: tasks.items || [],
    }
  }, [])

  const data = state.data ?? {
    systemInfo: null,
    providers: [],
    libraries: [],
    tasks: [],
  }

  return (
    <StatusBanner error={state.error} loading={state.loading}>
      <div className="page-grid two-col">
        <PageSection title="Overview" actions={<button onClick={state.refresh}>Refresh</button>}>
          <div className="stat-grid">
            <div className="stat-card"><span>Providers</span><strong>{data.providers.length}</strong></div>
            <div className="stat-card"><span>Libraries</span><strong>{data.libraries.length}</strong></div>
            <div className="stat-card"><span>Tasks</span><strong>{data.tasks.length}</strong></div>
          </div>
        </PageSection>
        <PageSection title="System Info">
          <div className="system-info-grid">
            <div className="system-hero-card">
              <span className="system-eyebrow">Service</span>
              <strong>{data.systemInfo?.name || 'NyaMedia'}</strong>
              <p>Current server bootstrap and storage paths for the admin service.</p>
            </div>
            <div className="info-field-grid">
              <InfoField label="Public Base URL" value={data.systemInfo?.public_base_url} mono />
              <InfoField label="Database Path" value={data.systemInfo?.database_path} mono />
              <InfoField label="STRM Output Dir" value={data.systemInfo?.strm_output_dir} mono />
            </div>
          </div>
        </PageSection>
      </div>
    </StatusBanner>
  )
}
