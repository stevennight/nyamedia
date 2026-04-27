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
    const [systemInfo, summary] = await Promise.all([
      api.systemInfo(),
      api.dashboardSummary(),
    ])
    return {
      systemInfo,
      providerCount: summary.provider_count ?? 0,
      libraryCount: summary.library_count ?? 0,
      taskTotal: summary.task_count ?? 0,
    }
  }, [])

  const data = state.data ?? {
    systemInfo: null,
    providerCount: 0,
    libraryCount: 0,
    taskTotal: 0,
  }

  return (
    <StatusBanner error={state.error} loading={state.loading}>
      <div className="page-grid two-col">
        <PageSection title="概览" actions={<button onClick={state.refresh}>刷新</button>}>
          <div className="stat-grid">
            <div className="stat-card"><span>数据源</span><strong>{data.providerCount}</strong></div>
            <div className="stat-card"><span>媒体库</span><strong>{data.libraryCount}</strong></div>
            <div className="stat-card"><span>任务</span><strong>{data.taskTotal}</strong></div>
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
              <InfoField label="服务端时间" value={data.systemInfo?.server_time} mono />
              <InfoField label="服务端时区" value={data.systemInfo?.server_timezone && `${data.systemInfo.server_timezone} (${data.systemInfo.server_utc_offset})`} mono />
              <InfoField label="数据库路径" value={data.systemInfo?.database_path} mono />
              <InfoField label="STRM 输出目录" value={data.systemInfo?.strm_output_dir} mono />
            </div>
          </div>
        </PageSection>
      </div>
    </StatusBanner>
  )
}
