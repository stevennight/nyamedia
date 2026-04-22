const byId = (id) => document.getElementById(id)

async function fetchJSON(url) {
  const response = await fetch(url)
  if (!response.ok) {
    throw new Error(`${response.status} ${response.statusText}`)
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

async function refreshAll() {
  try {
    await Promise.all([loadSystemInfo(), loadProviders(), loadLibraries(), loadTasks(), loadEntries()])
  } catch (error) {
    alert(error.message)
  }
}

byId('refresh-btn').addEventListener('click', refreshAll)
byId('entry-filter').addEventListener('submit', async (event) => {
  event.preventDefault()
  try {
    await loadEntries()
  } catch (error) {
    alert(error.message)
  }
})

refreshAll()
