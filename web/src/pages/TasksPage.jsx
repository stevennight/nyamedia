import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

const logPageSize = 50

function sortTasks(items) {
  return [...(items || [])].sort((left, right) => {
    const leftValue = left.created_at || left.started_at || ''
    const rightValue = right.created_at || right.started_at || ''
    const timeCompare = rightValue.localeCompare(leftValue)
    if (timeCompare !== 0) {
      return timeCompare
    }
    const leftId = left?.id || ''
    const rightId = right?.id || ''
    return rightId.localeCompare(leftId)
  })
}

function sortLogs(items) {
  return [...(items || [])].sort((left, right) => {
    const leftValue = left.created_at || ''
    const rightValue = right.created_at || ''
    const timeCompare = rightValue.localeCompare(leftValue)
    if (timeCompare !== 0) {
      return timeCompare
    }
    return (right.id || '').localeCompare(left.id || '')
  })
}

function normalizeTask(task) {
  return {
    id: task.id ?? task.ID ?? '',
    task_type: task.task_type ?? task.TaskType ?? '',
    library_id: task.library_id ?? task.LibraryID ?? '',
    status: task.status ?? task.Status ?? '',
    progress_done: task.progress_done ?? task.ProgressDone ?? 0,
    progress_total: task.progress_total ?? task.ProgressTotal ?? 0,
    message: task.message ?? task.Message ?? '',
    error_message: task.error_message ?? task.ErrorMessage ?? '',
    started_at: task.started_at ?? task.StartedAt ?? '',
    finished_at: task.finished_at ?? task.FinishedAt ?? '',
    created_at: task.created_at ?? task.CreatedAt ?? '',
    updated_at: task.updated_at ?? task.UpdatedAt ?? '',
  }
}

function normalizeTaskLog(log) {
  return {
    id: log.id ?? log.ID ?? '',
    task_id: log.task_id ?? log.TaskID ?? '',
    level: log.level ?? log.Level ?? '',
    message: log.message ?? log.Message ?? '',
    payload_json: log.payload_json ?? log.PayloadJSON ?? '',
    created_at: log.created_at ?? log.CreatedAt ?? '',
  }
}

function mergeLogs(current, incoming) {
  const byId = new Map()
  for (const item of current || []) {
    byId.set(item.id, item)
  }
  for (const item of incoming || []) {
    byId.set(item.id, item)
  }
  return sortLogs([...byId.values()])
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

function renderPayload(payload) {
  if (payload === null) return null
  if (typeof payload === 'string') {
    return <pre className="task-log-payload">{payload}</pre>
  }
  if (Array.isArray(payload)) {
    return <pre className="task-log-payload">{JSON.stringify(payload, null, 2)}</pre>
  }
  return (
    <div className="task-log-fields">
      {Object.entries(payload).map(([key, value]) => (
        <div key={key} className="task-log-field">
          <span>{key}</span>
          <strong>{formatValue(value)}</strong>
        </div>
      ))}
    </div>
  )
}

function logCursor(log) {
  if (!log) return {}
  return {
    created_at: log.created_at || '',
    id: log.id || '',
  }
}

export function TasksPage() {
  const [selectedTaskId, setSelectedTaskId] = useState('')
  const [logsOpen, setLogsOpen] = useState(false)
  const [logs, setLogs] = useState([])
  const [logsError, setLogsError] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [hasOlderLogs, setHasOlderLogs] = useState(false)
  const logListRef = useRef(null)
  const tasksState = useAsyncData(async () => ((await api.listTasks()).items || []).map(normalizeTask), [])
  const orderedTasks = sortTasks(tasksState.data)
  const selectedTask = orderedTasks.find((task) => task.id === selectedTaskId) || null

  async function refreshTasksQuietly() {
    const response = await api.listTasks()
    tasksState.setData((response.items || []).map(normalizeTask))
  }

  useEffect(() => {
    if (!selectedTaskId && orderedTasks.length) {
      setSelectedTaskId(orderedTasks[0].id)
    }
    if (selectedTaskId && !orderedTasks.some((task) => task.id === selectedTaskId)) {
      setSelectedTaskId(orderedTasks[0]?.id || '')
    }
  }, [orderedTasks, selectedTaskId])

  async function loadTaskLogs(mode = 'reset') {
    if (!selectedTaskId) return
    const newest = logs[0]
    const oldest = logs[logs.length - 1]
    const params = { limit: logPageSize }
    if (mode === 'older' && oldest?.id) {
      const cursor = logCursor(oldest)
      params.before_created_at = cursor.created_at
      params.before_id = cursor.id
    }
    if (mode === 'newer' && newest?.id) {
      const cursor = logCursor(newest)
      params.after_created_at = cursor.created_at
      params.after_id = cursor.id
    }

    if (mode === 'reset') {
      setLogsLoading(true)
    }
    if (mode === 'older') {
      setLoadingOlder(true)
    }
    if (mode !== 'newer') {
      setLogsError('')
    }

    try {
      const response = await api.listTaskLogs(selectedTaskId, params)
      const incoming = ((response.items || []).map(normalizeTaskLog))
      if (mode === 'reset') {
        setLogs(sortLogs(incoming))
      } else if (incoming.length > 0) {
        setLogs((current) => mergeLogs(current, incoming))
      }
      if (mode === 'older' || mode === 'reset') {
        setHasOlderLogs(Boolean(response.has_more))
      }
    } catch (error) {
      setLogsError(error.message)
    } finally {
      if (mode === 'reset') {
        setLogsLoading(false)
      }
      if (mode === 'older') {
        setLoadingOlder(false)
      }
    }
  }

  useEffect(() => {
    if (!logsOpen || !selectedTaskId) return undefined
    setLogs([])
    setHasOlderLogs(false)
    setLogsError('')
    refreshTasksQuietly().catch(() => {})
    loadTaskLogs('reset')
    return undefined
  }, [logsOpen, selectedTaskId])

  useEffect(() => {
    if (!logsOpen || !selectedTaskId) return undefined
    const timer = window.setInterval(() => {
      refreshTasksQuietly().catch(() => {})
      loadTaskLogs('newer')
    }, 3000)
    return () => window.clearInterval(timer)
  }, [logsOpen, selectedTaskId, logs])

  useEffect(() => {
    if (!logsOpen) return undefined
    const element = logListRef.current
    if (!element) return undefined

    function handleScroll() {
      if (logsLoading || loadingOlder || !hasOlderLogs) {
        return
      }
      const remaining = element.scrollHeight - element.scrollTop - element.clientHeight
      if (remaining <= 80) {
        loadTaskLogs('older')
      }
    }

    element.addEventListener('scroll', handleScroll)
    return () => element.removeEventListener('scroll', handleScroll)
  }, [logsOpen, logsLoading, loadingOlder, hasOlderLogs, logs])

  function handleOpenLogs(taskId) {
    setSelectedTaskId(taskId)
    setLogsOpen(true)
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="任务列表" actions={<button onClick={tasksState.refresh}>刷新任务</button>}>
        <StatusBanner error={tasksState.error} loading={tasksState.loading}>
          <div className="table-wrap">
            <table className="data-table tasks-table">
              <colgroup>
                <col className="task-type-col" />
                <col className="task-status-col" />
                <col className="task-library-col" />
                <col className="task-progress-col" />
                <col className="task-message-col" />
                <col className="task-time-col" />
                <col className="task-time-col" />
                <col className="task-action-col" />
              </colgroup>
              <thead>
                <tr>
                  <th>类型</th>
                  <th>状态</th>
                  <th>媒体库</th>
                  <th>进度</th>
                  <th>消息</th>
                  <th>开始时间</th>
                  <th>结束时间</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {orderedTasks.map((task) => (
                  <tr
                    key={task.id}
                    className={selectedTaskId === task.id ? 'row-selected' : ''}
                    onClick={() => setSelectedTaskId(task.id)}
                  >
                    <td>
                      <div>{task.task_type}</div>
                      <div className="subtle-id">{task.id}</div>
                    </td>
                    <td className="nowrap-cell">{task.status}</td>
                    <td className="break-cell">{task.library_id || '-'}</td>
                    <td className="nowrap-cell">{task.progress_done} / {task.progress_total}</td>
                    <td className="task-message-cell">{task.error_message || task.message || '-'}</td>
                    <td>{formatLocalDateTime(task.started_at)}</td>
                    <td>{formatLocalDateTime(task.finished_at)}</td>
                    <td className="nowrap-cell">
                      <div className="button-row">
                        <button type="button" onClick={(event) => { event.stopPropagation(); handleOpenLogs(task.id) }}>日志</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {orderedTasks.length === 0 ? (
                  <tr><td colSpan="8" className="empty-cell">暂无任务。</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>

      {logsOpen && selectedTask ? (
        <div className="modal-backdrop" role="presentation" onClick={() => setLogsOpen(false)}>
          <div className="modal-card task-logs-modal" role="dialog" aria-modal="true" aria-labelledby="task-logs-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header">
              <div>
                <h2 id="task-logs-title">任务日志</h2>
                <p>{selectedTask.task_type} · {selectedTask.id}</p>
              </div>
              <button type="button" className="ghost-button" onClick={() => setLogsOpen(false)}>关闭</button>
            </div>

            <div className="task-log-summary top-gap">
              <div className="info-field"><span>状态</span><strong>{selectedTask.status}</strong></div>
              <div className="info-field"><span>媒体库</span><strong>{selectedTask.library_id || '-'}</strong></div>
              <div className="info-field"><span>进度</span><strong>{selectedTask.progress_done} / {selectedTask.progress_total}</strong></div>
              <div className="info-field"><span>消息</span><strong>{selectedTask.error_message || selectedTask.message || '-'}</strong></div>
            </div>

            <section className="modal-section">
              <StatusBanner error={logsError} loading={logsLoading}>
                <div className="task-log-toolbar">
                  <div className="hint">最新在前，每 3 秒自动更新</div>
                  {loadingOlder ? <div className="hint">正在加载更早日志...</div> : null}
                </div>
                <div className="task-log-list" ref={logListRef}>
                  {logs.map((log) => {
                    const payload = parsePayload(log.payload_json)
                    return (
                      <article key={log.id} className="task-log-entry">
                        <div className="task-log-entry-header">
                          <span className={`task-log-level level-${log.level || 'info'}`}>{log.level || 'info'}</span>
                          <time className="mono-text">{formatLocalDateTime(log.created_at)}</time>
                        </div>
                        <h3>{log.message || '-'}</h3>
                        {payload !== null ? <div className="task-log-entry-body">{renderPayload(payload)}</div> : null}
                      </article>
                    )
                  })}
                  {logs.length === 0 && !logsLoading ? <div className="empty-cell">暂无日志。</div> : null}
                  {!hasOlderLogs && logs.length > 0 ? <div className="hint task-log-end">没有更早日志。</div> : null}
                </div>
              </StatusBanner>
            </section>
          </div>
        </div>
      ) : null}
    </div>
  )
}
