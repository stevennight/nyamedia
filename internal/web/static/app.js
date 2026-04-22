const byId = (id) => document.getElementById(id)

function setStatus(message, isError = false) {
  const el = byId('status')
  el.textContent = message
  el.style.color = isError ? '#fda4af' : '#9aa3b2'
}

async function fetchJSON(url) {
  const response = await fetch(url)
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const data = await response.json()
      if (data.error) {
        message = data.error
      }
    } catch (_) {
    }
    throw new Error(message)
  }
  return response.json()
}

async function sendJSON(url, method, body) {
  const response = await fetch(url, {
    method,
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(body)
  })

  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const data = await response.json()
      if (data.error) {
        message = data.error
      }
    } catch (_) {
    }
    throw new Error(message)
  }

  if (response.status === 204) {
    return null
  }

  return response.json()
}

function renderJSON(id, value) {
  byId(id).textContent = JSON.stringify(value, null, 2)
}

async function loadSystemInfo() {
  const info = await fetchJSON('/api/v1/system/info')
  byId('system-info').textContent = `${info.name} | ${info.public_base_url} | ${info.strm_output_dir}`
}

async function loadProviders() {
  const data = await fetchJSON('/api/v1/providers')
  renderJSON('providers', data.items || [])
}

async function loadSecrets() {
  const providerID = byId('secret-provider-id').value.trim()
  if (!providerID) {
    renderJSON('secrets', [])
    return
  }
  const data = await fetchJSON(`/api/v1/providers/${encodeURIComponent(providerID)}/secrets`)
  renderJSON('secrets', data.items || [])
}

async function loadLibraries() {
  const data = await fetchJSON('/api/v1/libraries')
  renderJSON('libraries', data.items || [])
}

async function loadTasks() {
  const data = await fetchJSON('/api/v1/tasks')
  renderJSON('tasks', data.items || [])
}

async function loadEntries() {
  const providerID = byId('provider-id').value.trim()
  const prefix = byId('prefix').value.trim()
  const limit = byId('limit').value.trim() || '100'
  const params = new URLSearchParams()
  if (providerID) params.set('provider_id', providerID)
  if (prefix) params.set('prefix', prefix)
  if (limit) params.set('limit', limit)
  const data = await fetchJSON(`/api/v1/entries?${params.toString()}`)
  renderJSON('entries', data.items || [])
}

async function loadMounts() {
  const libraryID = byId('mount-library-id').value.trim()
  if (!libraryID) {
    renderJSON('mount-result', 'Library id is required.')
    return
  }
  const data = await fetchJSON(`/api/v1/libraries/${encodeURIComponent(libraryID)}/mounts`)
  renderJSON('mount-result', data.items || [])
}

async function refreshAll() {
  try {
    await Promise.all([loadSystemInfo(), loadProviders(), loadLibraries(), loadTasks(), loadEntries(), loadSecrets()])
    setStatus(`Last refreshed at ${new Date().toLocaleTimeString()}`)
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function createProvider(event) {
  event.preventDefault()
  try {
    await sendJSON('/api/v1/providers', 'POST', {
      id: byId('provider-form-id').value.trim(),
      type: byId('provider-form-type').value,
      name: byId('provider-form-name').value.trim(),
      root_path: byId('provider-form-root').value.trim(),
      enabled: byId('provider-form-enabled').checked
    })
    event.target.reset()
    byId('provider-form-enabled').checked = true
    await Promise.all([loadProviders(), loadEntries()])
    setStatus('Provider created.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function createLibrary(event) {
  event.preventDefault()
  try {
    await sendJSON('/api/v1/libraries', 'POST', {
      id: byId('library-form-id').value.trim(),
      name: byId('library-form-name').value.trim(),
      description: byId('library-form-description').value.trim(),
      enabled: byId('library-form-enabled').checked
    })
    event.target.reset()
    byId('library-form-enabled').checked = true
    await loadLibraries()
    setStatus('Library created.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function createMount(event) {
  event.preventDefault()
  const libraryID = byId('mount-library-id').value.trim()
  try {
    const payload = {
      id: byId('mount-id').value.trim(),
      provider_id: byId('mount-provider-id').value.trim(),
      source_path: byId('mount-source-path').value.trim(),
      target_path: byId('mount-target-path').value.trim(),
      media_type: byId('mount-media-type').value.trim(),
      priority: Number(byId('mount-priority').value || '100'),
      enabled: byId('mount-enabled').checked
    }
    const data = await sendJSON(`/api/v1/libraries/${encodeURIComponent(libraryID)}/mounts`, 'POST', payload)
    renderJSON('mount-result', data)
    await loadMounts()
    setStatus('Mount created.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function saveSecret(event) {
  event.preventDefault()
  const providerID = byId('secret-provider-id').value.trim()
  const secretType = byId('secret-type').value.trim()
  const value = byId('secret-value').value
  try {
    await sendJSON(`/api/v1/providers/${encodeURIComponent(providerID)}/secrets/${encodeURIComponent(secretType)}`, 'PUT', { value })
    byId('secret-value').value = ''
    await loadSecrets()
    setStatus('Secret saved.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function deleteSecret() {
  const providerID = byId('secret-provider-id').value.trim()
  const secretType = byId('secret-type').value.trim()
  if (!providerID || !secretType) {
    setStatus('Provider id and secret type are required for delete.', true)
    return
  }
  try {
    await fetch(`/api/v1/providers/${encodeURIComponent(providerID)}/secrets/${encodeURIComponent(secretType)}`, { method: 'DELETE' }).then(async (response) => {
      if (!response.ok) {
        let message = `${response.status} ${response.statusText}`
        try {
          const data = await response.json()
          if (data.error) {
            message = data.error
          }
        } catch (_) {
        }
        throw new Error(message)
      }
    })
    await loadSecrets()
    setStatus('Secret deleted.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function deleteMount() {
  const libraryID = byId('mount-library-id').value.trim()
  const mountID = byId('mount-id').value.trim()
  if (!libraryID || !mountID) {
    setStatus('Library id and mount id are required for delete.', true)
    return
  }
  try {
    await fetch(`/api/v1/libraries/${encodeURIComponent(libraryID)}/mounts/${encodeURIComponent(mountID)}`, { method: 'DELETE' }).then(async (response) => {
      if (!response.ok) {
        let message = `${response.status} ${response.statusText}`
        try {
          const data = await response.json()
          if (data.error) {
            message = data.error
          }
        } catch (_) {
        }
        throw new Error(message)
      }
    })
    await loadMounts()
    setStatus('Mount deleted.')
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function runFullScan() {
  try {
    const task = await sendJSON('/api/v1/scan/full', 'POST', {})
    await loadTasks()
    setStatus(`Full scan queued: ${task.id}`)
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function runLibraryScan() {
  const libraryID = byId('mount-library-id').value.trim()
  if (!libraryID) {
    setStatus('Library id is required for library scan.', true)
    return
  }
  try {
    const task = await sendJSON(`/api/v1/scan/library/${encodeURIComponent(libraryID)}`, 'POST', {})
    await loadTasks()
    setStatus(`Library scan queued: ${task.id}`)
  } catch (error) {
    setStatus(error.message, true)
  }
}

async function refreshTasksOnly() {
  try {
    await loadTasks()
    setStatus(`Tasks refreshed at ${new Date().toLocaleTimeString()}`)
  } catch (error) {
    setStatus(error.message, true)
  }
}

byId('refresh-btn').addEventListener('click', refreshAll)
byId('provider-form').addEventListener('submit', createProvider)
byId('secret-form').addEventListener('submit', saveSecret)
byId('library-form').addEventListener('submit', createLibrary)
byId('mount-form').addEventListener('submit', createMount)
byId('load-secrets-btn').addEventListener('click', async () => {
  try {
    await loadSecrets()
    setStatus('Secrets loaded.')
  } catch (error) {
    setStatus(error.message, true)
  }
})
byId('delete-secret-btn').addEventListener('click', deleteSecret)
byId('scan-full-btn').addEventListener('click', runFullScan)
byId('load-mounts-btn').addEventListener('click', async () => {
  try {
    await loadMounts()
    setStatus('Mounts loaded.')
  } catch (error) {
    setStatus(error.message, true)
  }
})
byId('delete-mount-btn').addEventListener('click', deleteMount)
byId('scan-library-btn').addEventListener('click', runLibraryScan)
byId('refresh-tasks-btn').addEventListener('click', refreshTasksOnly)
byId('entry-filter').addEventListener('submit', async (event) => {
  event.preventDefault()
  try {
    await loadEntries()
    setStatus('Entries loaded.')
  } catch (error) {
    setStatus(error.message, true)
  }
})

refreshAll()
setInterval(loadTasks, 5000)
