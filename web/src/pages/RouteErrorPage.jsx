import { isRouteErrorResponse, useRouteError } from 'react-router-dom'

export function RouteErrorPage() {
  const error = useRouteError()

  let title = 'Something went wrong'
  let message = 'The page crashed while rendering.'

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
        <button type="button" onClick={() => window.location.assign('/admin/dashboard')}>Back to Admin</button>
      </div>
    </div>
  )
}
