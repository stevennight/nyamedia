import { useState } from 'react'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

export function EntriesPage() {
  const [filters, setFilters] = useState({ provider_id: '', prefix: '', limit: '50', page: 1 })
  const entriesState = useAsyncData(async () => {
    const params = {}
    if (filters.provider_id) params.provider_id = filters.provider_id
    if (filters.prefix) params.prefix = filters.prefix
    if (filters.limit) params.limit = filters.limit
    params.page = String(filters.page)
    return await api.listEntries(params)
  }, [filters.provider_id, filters.prefix, filters.limit, filters.page])

  const items = entriesState.data?.items || []
  const pagination = entriesState.data?.pagination || { page: filters.page, limit: Number(filters.limit), total: 0 }
  const totalPages = Math.max(1, Math.ceil((pagination.total || 0) / (pagination.limit || 1)))

  return (
    <div className="page-grid one-col">
      <PageSection title="Entry Filters" actions={<button onClick={entriesState.refresh}>Load</button>}>
        <div className="form-grid compact">
          <input value={filters.provider_id} onChange={(e) => setFilters({ ...filters, provider_id: e.target.value, page: 1 })} placeholder="provider id" />
          <input value={filters.prefix} onChange={(e) => setFilters({ ...filters, prefix: e.target.value, page: 1 })} placeholder="prefix" />
          <input type="number" min="1" max="1000" value={filters.limit} onChange={(e) => setFilters({ ...filters, limit: e.target.value, page: 1 })} />
        </div>
      </PageSection>
      <PageSection title="Entries">
        <StatusBanner error={entriesState.error} loading={entriesState.loading}>
          <div className="table-toolbar">
            <span>Total: {pagination.total}</span>
            <div className="button-row">
              <button disabled={filters.page <= 1} onClick={() => setFilters({ ...filters, page: filters.page - 1 })}>Prev</button>
              <span className="hint">Page {pagination.page} / {totalPages}</span>
              <button disabled={filters.page >= totalPages} onClick={() => setFilters({ ...filters, page: filters.page + 1 })}>Next</button>
            </div>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Name</th>
                  <th>Path</th>
                  <th>Size</th>
                  <th>Updated</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={`${item.provider_id}:${item.path}`}>
                    <td>{item.provider_id}</td>
                    <td>{item.name}</td>
                    <td>{item.path}</td>
                    <td>{item.size}</td>
                    <td>{formatLocalDateTime(item.updated_at)}</td>
                  </tr>
                ))}
                {items.length === 0 ? (
                  <tr>
                    <td colSpan="5" className="empty-cell">No entries found.</td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </StatusBanner>
      </PageSection>
    </div>
  )
}
