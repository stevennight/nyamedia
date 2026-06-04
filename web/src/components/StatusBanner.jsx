export function StatusBanner({ error, loading, children }) {
  if (loading) {
    return (
      <div className="loading-shell">
        <div className="loading-card" role="status" aria-live="polite">
          <span className="loading-spinner" aria-hidden="true" />
          <strong>正在进入后台</strong>
          <span>正在确认登录状态，请稍候。</span>
        </div>
      </div>
    )
  }
  if (error) {
    return <div className="banner banner-error">{error}</div>
  }
  return children
}
