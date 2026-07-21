import { useEffect, useState } from 'react'
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
  const [filterInputs, setFilterInputs] = useState({ provider_id: '', prefix: '', limit: '50' })
  const [query, setQuery] = useState({ provider_id: '', prefix: '', limit: '50', page: 1, cursors: [null] })

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setQuery((current) => ({ ...current, ...filterInputs, page: 1, cursors: [null] }))
    }, 300)
    return () => window.clearTimeout(timer)
  }, [filterInputs.provider_id, filterInputs.prefix, filterInputs.limit])

  const currentCursor = query.cursors[query.page - 1]
  const entriesState = useAsyncData(async (signal) => {
    const params = { pagination: 'cursor' }
    if (query.provider_id) params.provider_id = query.provider_id
    if (query.prefix) params.prefix = query.prefix
    if (query.limit) params.limit = query.limit
    if (currentCursor) {
      params.cursor_updated_at = currentCursor.updated_at
      params.cursor_provider_id = currentCursor.provider_id
      params.cursor_path = currentCursor.path
    }
    return await api.listEntries(params, { signal })
  }, [query.provider_id, query.prefix, query.limit, query.page, currentCursor?.updated_at, currentCursor?.provider_id, currentCursor?.path])

  function applyFilters() {
    const nextQuery = { ...filterInputs, page: 1, cursors: [null] }
    if (query.provider_id === nextQuery.provider_id && query.prefix === nextQuery.prefix && query.limit === nextQuery.limit && query.page === 1) {
      entriesState.refresh()
      return
    }
    setQuery(nextQuery)
  }

  function showNextPage() {
    const nextCursor = entriesState.data?.pagination?.next_cursor
    if (!nextCursor) return
    setQuery((current) => ({
      ...current,
      page: current.page + 1,
      cursors: [...current.cursors.slice(0, current.page), nextCursor],
    }))
  }

  const items = (entriesState.data?.items || []).map(normalizeEntry)
  const pagination = entriesState.data?.pagination || { limit: Number(query.limit), has_more: false, next_cursor: null }

  return (
    <div className="page-grid one-col">
      <PageSection title="条目筛选" actions={<button onClick={applyFilters}>加载</button>}>
        <div className="form-grid compact">
          <input value={filterInputs.provider_id} onChange={(e) => setFilterInputs({ ...filterInputs, provider_id: e.target.value })} placeholder="数据源 ID" />
          <input value={filterInputs.prefix} onChange={(e) => setFilterInputs({ ...filterInputs, prefix: e.target.value })} placeholder="路径前缀" />
          <input type="number" min="1" max="1000" value={filterInputs.limit} onChange={(e) => setFilterInputs({ ...filterInputs, limit: e.target.value })} />
        </div>
      </PageSection>
      <PageSection title="条目列表">
        <StatusBanner error={entriesState.error} loading={entriesState.loading}>
          <div className="table-toolbar pagination-bar">
            <div className="pagination-summary">
              <strong>{items.length}</strong>
              <span className="hint">本页 {items.length} 条条目</span>
            </div>
            <div className="pagination-controls">
              <span className="page-size-field static">
                <span>每页</span>
                <strong>{pagination.limit}</strong>
              </span>
              <div className="page-switcher">
                <button className="ghost-button" disabled={query.page <= 1} onClick={() => setQuery((current) => ({ ...current, page: current.page - 1 }))}>上一页</button>
                <span className="page-indicator">第 <strong>{query.page}</strong> 页</span>
                <button className="ghost-button" disabled={!pagination.has_more || !pagination.next_cursor} onClick={showNextPage}>下一页</button>
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
