import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

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

function parsePayload(value) {
  if (!value) return ''
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

export function TasksPage() {
  const [selectedTaskId, setSelectedTaskId] = useState('')
  const [logsOpen, setLogsOpen] = useState(false)
  const tasksState = useAsyncData(async () => ((await api.listTasks()).items || []).map(normalizeTask), [])
  const logsState = useAsyncData(async () => {
    if (!selectedTaskId) return []
    return ((await api.listTaskLogs(selectedTaskId, 2000)).items || []).map(normalizeTaskLog)
  }, [selectedTaskId])
  const orderedTasks = sortTasks(tasksState.data)
  const selectedTask = orderedTasks.find((task) => task.id === selectedTaskId) || null

  useEffect(() => {
    const timer = window.setInterval(() => tasksState.refresh(), 5000)
    return () => window.clearInterval(timer)
  }, [tasksState.refresh])

  useEffect(() => {
    if (!selectedTaskId && orderedTasks.length) {
      setSelectedTaskId(orderedTasks[0].id)
    }
    if (selectedTaskId && !orderedTasks.some((task) => task.id === selectedTaskId)) {
      setSelectedTaskId(orderedTasks[0]?.id || '')
    }
  }, [orderedTasks, selectedTaskId])

  useEffect(() => {
    if (!logsOpen || !selectedTaskId) return undefined
    const timer = window.setInterval(() => logsState.refresh(), 3000)
    return () => window.clearInterval(timer)
  }, [logsOpen, logsState.refresh, selectedTaskId])

  function handleOpenLogs(taskId) {
    setSelectedTaskId(taskId)
    setLogsOpen(true)
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="Task List" actions={<button onClick={tasksState.refresh}>Refresh Tasks</button>}>
        <StatusBanner error={tasksState.error} loading={tasksState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Library</th>
                  <th>Progress</th>
                  <th>Message</th>
                  <th>Started</th>
                  <th>Finished</th>
                  <th>Actions</th>
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
                    <td>{task.status}</td>
                    <td>{task.library_id || '-'}</td>
                    <td>{task.progress_done} / {task.progress_total}</td>
                    <td>{task.error_message || task.message || '-'}</td>
                    <td>{task.started_at}</td>
                    <td>{task.finished_at || '-'}</td>
                    <td>
                      <div className="button-row">
                        <button type="button" onClick={(event) => { event.stopPropagation(); handleOpenLogs(task.id) }}>Logs</button>
                      </div>
                    </td>
                  </tr>
                ))}
                {orderedTasks.length === 0 ? (
                  <tr><td colSpan="8" className="empty-cell">No tasks found.</td></tr>
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
                <h2 id="task-logs-title">Task Logs</h2>
                <p>{selectedTask.task_type} · {selectedTask.id}</p>
              </div>
              <div className="button-row">
                <button type="button" onClick={logsState.refresh}>Refresh Logs</button>
                <button type="button" className="ghost-button" onClick={() => setLogsOpen(false)}>Close</button>
              </div>
            </div>

            <div className="task-log-summary top-gap">
              <div className="info-field"><span>Status</span><strong>{selectedTask.status}</strong></div>
              <div className="info-field"><span>Library</span><strong>{selectedTask.library_id || '-'}</strong></div>
              <div className="info-field"><span>Progress</span><strong>{selectedTask.progress_done} / {selectedTask.progress_total}</strong></div>
              <div className="info-field"><span>Message</span><strong>{selectedTask.error_message || selectedTask.message || '-'}</strong></div>
            </div>

            <section className="modal-section">
              <StatusBanner error={logsState.error} loading={logsState.loading}>
                <div className="table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>Time</th>
                        <th>Level</th>
                        <th>Message</th>
                        <th>Details</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(logsState.data || []).map((log) => (
                        <tr key={log.id}>
                          <td>{log.created_at}</td>
                          <td>{log.level}</td>
                          <td>{log.message}</td>
                          <td>{log.payload_json ? <pre className="task-log-payload">{parsePayload(log.payload_json)}</pre> : '-'}</td>
                        </tr>
                      ))}
                      {(logsState.data || []).length === 0 ? (
                        <tr><td colSpan="4" className="empty-cell">No logs found.</td></tr>
                      ) : null}
                    </tbody>
                  </table>
                </div>
              </StatusBanner>
            </section>
          </div>
        </div>
      ) : null}
    </div>
  )
}
