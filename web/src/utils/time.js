function createDateTimeFormatter(timeZone) {
  return new Intl.DateTimeFormat(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
    ...(timeZone ? { timeZone } : {}),
  })
}

const localDateTimeFormatter = createDateTimeFormatter()
const formatterCache = new Map()

function dateTimeFormatter(timeZone) {
  if (!timeZone) return localDateTimeFormatter
  if (!formatterCache.has(timeZone)) {
    formatterCache.set(timeZone, createDateTimeFormatter(timeZone))
  }
  return formatterCache.get(timeZone)
}

export function formatLocalDateTime(value, timeZone = '') {
  if (!value) return '-'
  const normalized = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(value)
    ? value.replace(' ', 'T') + 'Z'
    : value
  const parsed = new Date(normalized)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return dateTimeFormatter(timeZone).format(parsed)
}

export function isValidTimeZone(value) {
  if (!value) return false
  try {
    createDateTimeFormatter(value).format(new Date())
    return true
  } catch {
    return false
  }
}
