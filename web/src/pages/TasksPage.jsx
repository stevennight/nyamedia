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

  return (
    <div className="page-grid two-col">
      <PageSection title="Scan Actions" actions={<button onClick={tasksState.refresh}>Refresh Tasks</button>}>
        <div className="button-row">
          <button onClick={() => api.runFullScan().then(tasksState.refresh)}>Run Full Scan</button>
        </div>
      </PageSection>
      <PageSection title="Task List">
        <StatusBanner error={tasksState.error} loading={tasksState.loading}>
          <div className="form-grid compact">
            <input value={selectedTaskId} onChange={(e) => setSelectedTaskId(e.target.value)} placeholder="task id for logs" />
            <button onClick={logsState.refresh}>Load Logs</button>
          </div>
          <JsonBlock value={tasksState.data} />
        </StatusBanner>
      </PageSection>
      <PageSection title="Task Logs">
        <StatusBanner error={logsState.error} loading={logsState.loading}>
          <JsonBlock value={logsState.data} />
        </StatusBanner>
      </PageSection>
    </div>
  )
}
