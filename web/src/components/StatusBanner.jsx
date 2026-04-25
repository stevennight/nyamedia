export function StatusBanner({ error, loading, children }) {
  if (loading) {
    return <div className="banner">加载中...</div>
  }
  if (error) {
    return <div className="banner banner-error">{error}</div>
  }
  return children
}
