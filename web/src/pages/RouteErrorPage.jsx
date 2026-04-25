import { isRouteErrorResponse, useRouteError } from 'react-router-dom'

export function RouteErrorPage() {
  const error = useRouteError()

  let title = '页面出错'
  let message = '页面渲染时发生错误。'

  if (isRouteErrorResponse(error)) {
    title = `${error.status} ${error.statusText}`
    message = error.data?.error || error.data || message
  } else if (error instanceof Error) {
    message = error.message
  }

  return (
    <div className="login-shell">
      <div className="login-card">
        <h1>{title}</h1>
        <p>{message}</p>
        <button type="button" onClick={() => window.location.assign('/admin/dashboard')}>返回管理后台</button>
      </div>
    </div>
  )
}
