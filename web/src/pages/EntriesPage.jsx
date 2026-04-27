import { useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { formatLocalDateTime } from '../utils/time'

function normalizeEntry(entry) {
  return {
    id: entry.id ?? entry.ID ?? '',
    provider_id: entry.provider_id ?? entry.ProviderID ?? '',
    entry_type: entry.entry_type ?? entry.EntryType ?? '',
    path: entry.path ?? entry.Path ?? '',
    parent_path: entry.parent_path ?? entry.ParentPath ?? '',
    name: entry.name ?? entry.Name ?? '',
    size: entry.size ?? entry.Size ?? 0,
    mtime: entry.mtime ?? entry.MTime ?? '',
    mime_type: entry.mime_type ?? entry.MimeType ?? '',
    content_hash: entry.content_hash ?? entry.ContentHash ?? '',
    provider_entry_id: entry.provider_entry_id ?? entry.ProviderEntryID ?? '',
    metadata_json: entry.metadata_json ?? entry.MetadataJSON ?? '',
    last_seen_at: entry.last_seen_at ?? entry.LastSeenAt ?? '',
    created_at: entry.created_at ?? entry.CreatedAt ?? '',
    updated_at: entry.updated_at ?? entry.UpdatedAt ?? '',
  }
}

export function EntriesPage() {
  const { systemTimeZone } = useOutletContext() || {}
  const [filters, setFilters] = useState({ provider_id: '', prefix: '', limit: '50', page: 1 })
  const entriesState = useAsyncData(async () => {
    const params = {}
    if (filters.provider_id) params.provider_id = filters.provider_id
    if (filters.prefix) params.prefix = filters.prefix
    if (filters.limit) params.limit = filters.limit
    params.page = String(filters.page)
    return await api.listEntries(params)
  }, [filters.provider_id, filters.prefix, filters.limit, filters.page])

  const items = (entriesState.data?.items || []).map(normalizeEntry)
  const pagination = entriesState.data?.pagination || { page: filters.page, limit: Number(filters.limit), total: 0 }
  const totalPages = Math.max(1, Math.ceil((pagination.total || 0) / (pagination.limit || 1)))

  return (
    <div className="page-grid one-col">
      <PageSection title="条目筛选" actions={<button onClick={entriesState.refresh}>加载</button>}>
        <div className="form-grid compact">
          <input value={filters.provider_id} onChange={(e) => setFilters({ ...filters, provider_id: e.target.value, page: 1 })} placeholder="数据源 ID" />
          <input value={filters.prefix} onChange={(e) => setFilters({ ...filters, prefix: e.target.value, page: 1 })} placeholder="路径前缀" />
          <input type="number" min="1" max="1000" value={filters.limit} onChange={(e) => setFilters({ ...filters, limit: e.target.value, page: 1 })} />
        </div>
      </PageSection>
      <PageSection title="条目列表">
        <StatusBanner error={entriesState.error} loading={entriesState.loading}>
          <div className="table-toolbar pagination-bar">
            <div className="pagination-summary">
              <strong>{pagination.total}</strong>
              <span className="hint">共 {pagination.total} 条条目</span>
            </div>
            <div className="pagination-controls">
              <span className="page-size-field static">
                <span>每页</span>
                <strong>{pagination.limit}</strong>
              </span>
              <div className="page-switcher">
                <button className="ghost-button" disabled={filters.page <= 1} onClick={() => setFilters({ ...filters, page: filters.page - 1 })}>上一页</button>
                <span className="page-indicator">第 <strong>{pagination.page}</strong> / {totalPages} 页</span>
                <button className="ghost-button" disabled={filters.page >= totalPages} onClick={() => setFilters({ ...filters, page: filters.page + 1 })}>下一页</button>
              </div>
            </div>
          </div>
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>数据源</th>
                  <th>名称</th>
                  <th>路径</th>
                  <th>大小</th>
                  <th>更新时间</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={`${item.provider_id}:${item.path}`}>
                    <td>{item.provider_id}</td>
                    <td>{item.name}</td>
                    <td>{item.path}</td>
                    <td>{item.size}</td>
                    <td>{formatLocalDateTime(item.updated_at, systemTimeZone)}</td>
                  </tr>
                ))}
                {items.length === 0 ? (
                  <tr>
                    <td colSpan="5" className="empty-cell">暂无条目。</td>
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
