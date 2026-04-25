export const themeStorageKey = 'nyamedia.theme'

export const themeModes = ['system', 'light', 'dark']

export function getStoredThemeMode() {
  const value = window.localStorage.getItem(themeStorageKey)
  return themeModes.includes(value) ? value : 'system'
}

export function resolveTheme(mode) {
  if (mode === 'light' || mode === 'dark') {
    return mode
  }
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function applyThemeMode(mode) {
  const nextMode = themeModes.includes(mode) ? mode : 'system'
  const resolved = resolveTheme(nextMode)
  document.documentElement.dataset.themeMode = nextMode
  document.documentElement.dataset.theme = resolved
  return resolved
}

export function saveThemeMode(mode) {
  const nextMode = themeModes.includes(mode) ? mode : 'system'
  window.localStorage.setItem(themeStorageKey, nextMode)
  return applyThemeMode(nextMode)
}
