import React from 'react'
import ReactDOM from 'react-dom/client'
import { createBrowserRouter, Navigate, RouterProvider } from 'react-router-dom'
import { AdminLayout } from './shell/AdminLayout'
import { ProtectedLayout } from './shell/ProtectedLayout'
import { DashboardPage } from './pages/DashboardPage'
import { ProvidersPage } from './pages/ProvidersPage'
import { LibrariesPage } from './pages/LibrariesPage'
import { TasksPage } from './pages/TasksPage'
import { EntriesPage } from './pages/EntriesPage'
import { SettingsPage } from './pages/SettingsPage'
import { LoginPage } from './pages/LoginPage'
import { RouteErrorPage } from './pages/RouteErrorPage'
import './styles.css'

const router = createBrowserRouter([
  {
    path: '/admin/login',
    element: <LoginPage />,
    errorElement: <RouteErrorPage />,
  },
  {
    path: '/admin',
    element: (
      <ProtectedLayout>
        <AdminLayout />
      </ProtectedLayout>
    ),
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, element: <Navigate to="dashboard" replace /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'providers', element: <ProvidersPage /> },
      { path: 'libraries', element: <LibrariesPage /> },
      { path: 'tasks', element: <TasksPage /> },
      { path: 'entries', element: <EntriesPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
  { path: '/', element: <Navigate to="/admin/dashboard" replace />, errorElement: <RouteErrorPage /> },
])

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <RouterProvider router={router} />
  </React.StrictMode>,
)
