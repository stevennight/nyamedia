import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

export function TasksPage() {
  const [selectedTaskId, setSelectedTaskId] = useState('')
  const tasksState = useAsyncData(async () => (await api.listTasks()).items || [], [])
  const logsState = useAsyncData(async () => {
    if (!selectedTaskId) return []
    return (await api.listTaskLogs(selectedTaskId)).items || []
  }, [selectedTaskId])

  useEffect(() => {
    const timer = window.setInterval(() => tasksState.refresh(), 5000)
    return () => window.clearInterval(timer)
  }, [tasksState.refresh])

  useEffect(() => {
    if (!selectedTaskId && tasksState.data?.length) {
      setSelectedTaskId(tasksState.data[0].id)
    }
  }, [tasksState.data, selectedTaskId])

  async function handleRunFullScan() {
    try {
      await api.runFullScan()
      await tasksState.refresh()
    } catch (error) {
      alert(error.message)
    }
  }

  return (
    <div className="page-grid two-col">
      <PageSection title="Scan Actions" actions={<button onClick={tasksState.refresh}>Refresh Tasks</button>}>
        <div className="button-row">
          <button onClick={handleRunFullScan}>Run Full Scan</button>
        </div>
      </PageSection>
      <PageSection title="Task List">
        <StatusBanner error={tasksState.error} loading={tasksState.loading}>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Library</th>
                  <th>Progress</th>
                  <th>Started</th>
                  <th>Finished</th>
                </tr>
              </thead>
              <tbody>
                {(tasksState.data || []).map((task) => (
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
                    <td>{task.started_at}</td>
                    <td>{task.finished_at || '-'}</td>
                  </tr>
                ))}
                {(tasksState.data || []).length === 0 ? (
                  <tr><td colSpan="6" className="empty-cell">No tasks found.</td></tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>
      <PageSection title={`Task Logs${selectedTaskId ? ` · ${selectedTaskId}` : ''}`} actions={<button onClick={logsState.refresh}>Refresh Logs</button>}>
        <StatusBanner error={logsState.error} loading={logsState.loading}>
          <JsonBlock value={logsState.data} />
        </StatusBanner>
      </PageSection>
    </div>
  )
}
