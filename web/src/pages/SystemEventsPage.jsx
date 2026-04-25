import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

function normalizeEvent(event) {
  return {
    id: event.id ?? event.ID ?? '',
    event_type: event.event_type ?? event.EventType ?? '',
    level: event.level ?? event.Level ?? 'info',
    source: event.source ?? event.Source ?? '',
    message: event.message ?? event.Message ?? '',
    payload_json: event.payload_json ?? event.PayloadJSON ?? '',
    created_at: event.created_at ?? event.CreatedAt ?? '',
  }
}

function parsePayload(value) {
  if (!value) return null
  try {
    return JSON.parse(value)
  } catch {
    return value
  }
}

function formatValue(value) {
  if (value === null || value === undefined || value === '') return '-'
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return String(value)
  return JSON.stringify(value, null, 2)
}

const payloadLabels = {
  endpoint: '入口',
  method: '方法',
  remote_addr: '来源地址',
  user_agent: 'User-Agent',
  event: '事件',
  path: '路径',
  destination_path: '目标路径',
  provider_id: '数据源',
  provider_type: '数据源类型',
  provider_name: '数据源名称',
  library_id: '媒体库',
  matched: '匹配数',
  queued: '入队数',
  stage: '阶段',
  auth_type: '授权类型',
  error: '错误',
  cutoff: '清理截止时间',
  retention_days: '保留天数',
  tasks: '任务数',
  events: '事件数',
  payload: '原始载荷',
}

function renderPayload(payload) {
  if (payload === null) return null
  if (typeof payload === 'string' || Array.isArray(payload)) {
    return <pre className="task-log-payload">{formatValue(payload)}</pre>
  }
  return (
    <div className="task-log-fields">
      {Object.entries(payload).map(([key, value]) => (
        <div key={key} className="task-log-field">
          <span>{payloadLabels[key] || key}</span>
          <strong>{formatValue(value)}</strong>
        </div>
      ))}
    </div>
  )
}

function formatEventTitle(event) {
  switch (event.event_type) {
    case 'webhook_received': return 'Webhook 请求已收到'
    case 'webhook_auth_failed': return 'Webhook 鉴权失败'
    case 'webhook_payload_error': return 'Webhook 载荷错误'
    case 'webhook_no_match': return 'Webhook 未匹配挂载'
    case 'webhook_queued': return 'Webhook 已加入扫描队列'
    case 'webhook_method_not_allowed': return 'Webhook 请求方法不支持'
    case 'provider_auth_error': return '数据源授权异常'
    case 'scan_log_pruned': return '扫描日志已清理'
    case 'scan_log_prune_error': return '扫描日志清理失败'
    case 'system_events_pruned': return '系统事件已清理'
    case 'system_event_prune_error': return '系统事件清理失败'
    case 'provider_watch_started': return '监听已启动'
    case 'provider_watch_stopped': return '监听已停止'
    case 'provider_watch_change': return '检测到数据源变更'
    case 'provider_watch_error': return '监听异常'
    default: return event.message || event.event_type || '-'
  }
}

export function SystemEventsPage() {
  const eventsState = useAsyncData(async () => ((await api.listSystemEvents(200)).items || []).map(normalizeEvent), [])

  return (
    <div className="page-grid one-col">
      <PageSection title="系统事件" actions={<button onClick={eventsState.refresh}>刷新事件</button>}>
        <StatusBanner error={eventsState.error} loading={eventsState.loading}>
          <div className="task-log-list system-event-list">
            {(eventsState.data || []).map((event) => {
              const payload = parsePayload(event.payload_json)
              return (
                <article key={event.id} className="task-log-entry">
                  <div className="task-log-entry-header">
                    <div className="event-title-row">
                      <span className={`task-log-level level-${event.level || 'info'}`}>{event.level || 'info'}</span>
                      <strong>{formatEventTitle(event)}</strong>
                    </div>
                    <time className="mono-text">{formatLocalDateTime(event.created_at)}</time>
                  </div>
                  <h3>{event.source || '-'} · {event.event_type || '-'}</h3>
                  {payload !== null ? <div className="task-log-entry-body">{renderPayload(payload)}</div> : null}
                </article>
              )
            })}
            {eventsState.data?.length === 0 && !eventsState.loading ? <div className="empty-cell">暂无系统事件。</div> : null}
          </div>
        </StatusBanner>
      </PageSection>
    </div>
  )
}
