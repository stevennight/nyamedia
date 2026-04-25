export async function apiFetch(path, options = {}) {
  const response = await fetch(path, {
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
    ...options,
  })

  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const data = await response.json()
      if (data.error) {
        message = data.error
      }
    } catch {
    }
    throw new Error(message)
  }

  if (response.status === 204) {
    return null
  }

  return response.json()
}

export const api = {
  login: (payload) => apiFetch('/api/v1/auth/login', { method: 'POST', body: JSON.stringify(payload) }),
  logout: () => apiFetch('/api/v1/auth/logout', { method: 'POST', body: '{}' }),
  me: () => apiFetch('/api/v1/auth/me'),
  updateMe: (payload) => apiFetch('/api/v1/auth/me/account', { method: 'PUT', body: JSON.stringify(payload) }),
  systemInfo: () => apiFetch('/api/v1/system/info'),
  listDirectories: (path = '') => apiFetch(`/api/v1/filesystem/directories${path ? `?${new URLSearchParams({ path }).toString()}` : ''}`),
  createDirectory: (path, name) => apiFetch('/api/v1/filesystem/directories', { method: 'POST', body: JSON.stringify({ path, name }) }),
  listOutputDirectories: (path = '') => apiFetch(`/api/v1/filesystem/output-directories${path ? `?${new URLSearchParams({ path }).toString()}` : ''}`),
  createOutputDirectory: (path, name) => apiFetch('/api/v1/filesystem/output-directories', { method: 'POST', body: JSON.stringify({ path, name }) }),
  listEmbyServers: () => apiFetch('/api/v1/emby-servers'),
  createEmbyServer: (payload) => apiFetch('/api/v1/emby-servers', { method: 'POST', body: JSON.stringify(payload) }),
  updateEmbyServer: (key, payload) => apiFetch(`/api/v1/emby-servers/${encodeURIComponent(key)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteEmbyServer: (key) => apiFetch(`/api/v1/emby-servers/${encodeURIComponent(key)}`, { method: 'DELETE' }),
  listProviders: () => apiFetch('/api/v1/providers'),
  createProvider: (payload) => apiFetch('/api/v1/providers', { method: 'POST', body: JSON.stringify(payload) }),
  updateProvider: (id, payload) => apiFetch(`/api/v1/providers/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteProvider: (id) => apiFetch(`/api/v1/providers/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  listProviderSecrets: (providerId) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/secrets`),
  saveProviderSecret: (providerId, type, value) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/secrets/${encodeURIComponent(type)}`, { method: 'PUT', body: JSON.stringify({ value }) }),
  deleteProviderSecret: (providerId, type) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/secrets/${encodeURIComponent(type)}`, { method: 'DELETE' }),
  startProvider115OpenAuth: (providerId, clientId) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/auth/115open`, { method: 'POST', body: JSON.stringify({ client_id: clientId || '' }) }),
  getProvider115OpenAuthStatus: (providerId, sessionId) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/auth/115open?session_id=${encodeURIComponent(sessionId)}`),
  startProvider115CookieAuth: (providerId, terminal) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/auth/115cookie`, { method: 'POST', body: JSON.stringify({ terminal }) }),
  getProvider115CookieAuthStatus: (providerId, sessionId) => apiFetch(`/api/v1/providers/${encodeURIComponent(providerId)}/auth/115cookie?session_id=${encodeURIComponent(sessionId)}`),
  listLibraries: () => apiFetch('/api/v1/libraries'),
  createLibrary: (payload) => apiFetch('/api/v1/libraries', { method: 'POST', body: JSON.stringify(payload) }),
  updateLibrary: (id, payload) => apiFetch(`/api/v1/libraries/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteLibrary: (id, options = {}) => apiFetch(`/api/v1/libraries/${encodeURIComponent(id)}${options.cleanup_outputs ? '?cleanup_outputs=true' : ''}`, { method: 'DELETE' }),
  listMounts: (libraryId) => apiFetch(`/api/v1/libraries/${encodeURIComponent(libraryId)}/mounts`),
  createMount: (libraryId, payload) => apiFetch(`/api/v1/libraries/${encodeURIComponent(libraryId)}/mounts`, { method: 'POST', body: JSON.stringify(payload) }),
  updateMount: (libraryId, mountId, payload) => apiFetch(`/api/v1/libraries/${encodeURIComponent(libraryId)}/mounts/${encodeURIComponent(mountId)}`, { method: 'PUT', body: JSON.stringify(payload) }),
  deleteMount: (libraryId, mountId, options = {}) => apiFetch(`/api/v1/libraries/${encodeURIComponent(libraryId)}/mounts/${encodeURIComponent(mountId)}${options.cleanup_outputs ? '?cleanup_outputs=true' : ''}`, { method: 'DELETE' }),
  listTasks: () => apiFetch('/api/v1/tasks'),
  listTaskLogs: (taskId, params = {}) => {
    const search = new URLSearchParams()
    if (params.limit) search.set('limit', String(params.limit))
    if (params.before_created_at) search.set('before_created_at', params.before_created_at)
    if (params.before_id) search.set('before_id', params.before_id)
    if (params.after_created_at) search.set('after_created_at', params.after_created_at)
    if (params.after_id) search.set('after_id', params.after_id)
    const query = search.toString()
    return apiFetch(`/api/v1/tasks/${encodeURIComponent(taskId)}/logs${query ? `?${query}` : ''}`)
  },
  runFullScan: () => apiFetch('/api/v1/scan/full', { method: 'POST', body: '{}' }),
  runLibraryScan: (libraryId, payload = {}) => apiFetch(`/api/v1/scan/library/${encodeURIComponent(libraryId)}`, { method: 'POST', body: JSON.stringify(payload) }),
  listEntries: (params) => apiFetch(`/api/v1/entries?${new URLSearchParams(params).toString()}`),
  listSettings: () => apiFetch('/api/v1/settings'),
  upsertSetting: (key, value) => apiFetch(`/api/v1/settings/${encodeURIComponent(key)}`, { method: 'PUT', body: JSON.stringify({ value }) }),
  deleteSetting: (key) => apiFetch(`/api/v1/settings/${encodeURIComponent(key)}`, { method: 'DELETE' }),
}
