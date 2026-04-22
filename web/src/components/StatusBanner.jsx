export function StatusBanner({ error, loading, children }) {
  if (loading) {
    return <div className="banner">Loading...</div>
  }
  if (error) {
    return <div className="banner banner-error">{error}</div>
  }
  return children
}
