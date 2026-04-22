import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

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
          <JsonBlock value={data.systemInfo} />
        </PageSection>
      </div>
    </StatusBanner>
  )
}
