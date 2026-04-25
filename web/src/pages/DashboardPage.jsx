import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

function InfoField({ label, value, mono = false }) {
  return (
    <div className="info-field">
      <span>{label}</span>
      <strong className={mono ? 'mono-text' : undefined}>{value || '未设置'}</strong>
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
        <PageSection title="概览" actions={<button onClick={state.refresh}>刷新</button>}>
          <div className="stat-grid">
            <div className="stat-card"><span>数据源</span><strong>{data.providers.length}</strong></div>
            <div className="stat-card"><span>媒体库</span><strong>{data.libraries.length}</strong></div>
            <div className="stat-card"><span>任务</span><strong>{data.tasks.length}</strong></div>
          </div>
        </PageSection>
        <PageSection title="系统信息">
          <div className="system-info-grid">
            <div className="system-hero-card">
              <span className="system-eyebrow">服务</span>
              <strong>{data.systemInfo?.name || 'NyaMedia'}</strong>
              <p>当前服务启动配置和存储路径。</p>
            </div>
            <div className="info-field-grid">
              <InfoField label="公网访问地址" value={data.systemInfo?.public_base_url} mono />
              <InfoField label="数据库路径" value={data.systemInfo?.database_path} mono />
              <InfoField label="STRM 输出目录" value={data.systemInfo?.strm_output_dir} mono />
            </div>
          </div>
        </PageSection>
      </div>
    </StatusBanner>
  )
}
