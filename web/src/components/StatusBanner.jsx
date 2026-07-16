export function StatusBanner({ error, loading, fullScreen = false, children }) {
  if (loading) {
    return (
      <div className={fullScreen ? 'loading-shell' : 'loading-inline'}>
        <div className="loading-card" role="status" aria-live="polite">
          <span className="loading-spinner" aria-hidden="true" />
          <strong>加载中</strong>
        </div>
      </div>
    )
  }
  if (error) {
    return <div className="banner banner-error">{error}</div>
  }
  return children
}
