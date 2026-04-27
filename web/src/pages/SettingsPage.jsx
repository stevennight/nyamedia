import { useEffect, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { api } from '../api/client'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'
import { isValidTimeZone } from '../utils/time'
import { applyThemeMode, getStoredThemeMode, saveThemeMode } from '../utils/theme'

const scanLogRetentionSettingKey = 'scan.log_retention_days'
const systemEventRetentionSettingKey = 'system.event_retention_days'
const systemTimezoneSettingKey = 'system.timezone'

function parseSettingValue(valueJSON, fallback = '') {
  if (typeof valueJSON !== 'string') {
    return fallback
  }

  try {
    return JSON.parse(valueJSON)
  } catch {
    return fallback
  }
}

function normalizeSettings(items) {
  const next = {}
  for (const item of items || []) {
    const key = item.key || item.Key
    if (!key) continue
    next[key] = parseSettingValue(item.value_json ?? item.ValueJSON)
  }
  return next
}

export function SettingsPage() {
  const { systemTimeZone, setSystemTimeZone } = useOutletContext() || {}
  const [themeMode, setThemeMode] = useState(() => getStoredThemeMode())
  const [accountForm, setAccountForm] = useState({ username: '', currentPassword: '', newPassword: '', confirmPassword: '' })
  const [accountError, setAccountError] = useState('')
  const [accountSuccess, setAccountSuccess] = useState('')
  const [accountSubmitting, setAccountSubmitting] = useState(false)
  const [retentionDays, setRetentionDays] = useState('0')
  const [retentionError, setRetentionError] = useState('')
  const [retentionSuccess, setRetentionSuccess] = useState('')
  const [retentionSubmitting, setRetentionSubmitting] = useState(false)
  const [eventRetentionDays, setEventRetentionDays] = useState('0')
  const [eventRetentionError, setEventRetentionError] = useState('')
  const [eventRetentionSuccess, setEventRetentionSuccess] = useState('')
  const [eventRetentionSubmitting, setEventRetentionSubmitting] = useState(false)
  const [timeZone, setTimeZone] = useState('')
  const [timeZoneError, setTimeZoneError] = useState('')
  const [timeZoneSuccess, setTimeZoneSuccess] = useState('')
  const [timeZoneSubmitting, setTimeZoneSubmitting] = useState(false)
  const authState = useAsyncData(() => api.me(), [])
  const settingsState = useAsyncData(async () => (await api.listSettings()).items || [], [])

  useEffect(() => {
    if (!authState.data?.username) {
      return
    }
    setAccountForm((current) => (current.username ? current : { ...current, username: authState.data.username }))
  }, [authState.data])

  useEffect(() => {
    applyThemeMode(themeMode)
    if (themeMode !== 'system') {
      return undefined
    }

    const media = window.matchMedia('(prefers-color-scheme: light)')
    const handleChange = () => applyThemeMode('system')
    media.addEventListener('change', handleChange)
    return () => media.removeEventListener('change', handleChange)
  }, [themeMode])

  useEffect(() => {
    const settingsMap = normalizeSettings(settingsState.data)
    const value = settingsMap[scanLogRetentionSettingKey]
    const eventValue = settingsMap[systemEventRetentionSettingKey]
    const timezoneValue = settingsMap[systemTimezoneSettingKey]
    setRetentionDays(Number.isFinite(Number(value)) ? String(Number(value)) : '0')
    setEventRetentionDays(Number.isFinite(Number(eventValue)) ? String(Number(eventValue)) : '0')
    setTimeZone(typeof timezoneValue === 'string' ? timezoneValue : systemTimeZone || '')
  }, [settingsState.data, systemTimeZone])

  function handleThemeModeChange(event) {
    const nextMode = event.target.value
    setThemeMode(nextMode)
    saveThemeMode(nextMode)
  }

  async function handleAccountSave(event) {
    event.preventDefault()
    setAccountError('')
    setAccountSuccess('')

    if (accountForm.newPassword && accountForm.newPassword !== accountForm.confirmPassword) {
      setAccountError('两次输入的新密码不一致')
      return
    }

    setAccountSubmitting(true)
    try {
      const data = await api.updateMe({
        username: accountForm.username,
        current_password: accountForm.currentPassword,
        new_password: accountForm.newPassword,
      })
      setAccountForm((current) => ({ ...current, username: data.username, currentPassword: '', newPassword: '', confirmPassword: '' }))
      setAccountSuccess('账号信息已更新')
      authState.refresh()
    } catch (err) {
      setAccountError(err instanceof Error ? err.message : String(err))
    } finally {
      setAccountSubmitting(false)
    }
  }

  async function handleRetentionSave(event) {
    event.preventDefault()
    setRetentionError('')
    setRetentionSuccess('')

    const days = Number(retentionDays)
    if (!Number.isInteger(days) || days < 0 || days > 3650) {
      setRetentionError('保留天数必须是 0 到 3650 之间的整数')
      return
    }

    setRetentionSubmitting(true)
    try {
      await api.upsertSetting(scanLogRetentionSettingKey, days)
      setRetentionSuccess(days === 0 ? '扫描日志自动清理已禁用' : `扫描日志将保留 ${days} 天`)
      settingsState.refresh()
    } catch (err) {
      setRetentionError(err instanceof Error ? err.message : String(err))
    } finally {
      setRetentionSubmitting(false)
    }
  }

  async function handleEventRetentionSave(event) {
    event.preventDefault()
    setEventRetentionError('')
    setEventRetentionSuccess('')

    const days = Number(eventRetentionDays)
    if (!Number.isInteger(days) || days < 0 || days > 3650) {
      setEventRetentionError('保留天数必须是 0 到 3650 之间的整数')
      return
    }

    setEventRetentionSubmitting(true)
    try {
      await api.upsertSetting(systemEventRetentionSettingKey, days)
      setEventRetentionSuccess(days === 0 ? '系统事件自动清理已禁用' : `系统事件将保留 ${days} 天`)
      settingsState.refresh()
    } catch (err) {
      setEventRetentionError(err instanceof Error ? err.message : String(err))
    } finally {
      setEventRetentionSubmitting(false)
    }
  }

  async function handleTimeZoneSave(event) {
    event.preventDefault()
    setTimeZoneError('')
    setTimeZoneSuccess('')

    const value = timeZone.trim()
    if (!isValidTimeZone(value)) {
      setTimeZoneError('请输入有效的 IANA 时区，例如 Asia/Shanghai 或 UTC')
      return
    }

    setTimeZoneSubmitting(true)
    try {
      const item = await api.upsertSetting(systemTimezoneSettingKey, value)
      const savedValue = parseSettingValue(item.value_json ?? item.ValueJSON, value)
      setTimeZone(savedValue)
      setSystemTimeZone?.(savedValue)
      setTimeZoneSuccess(`系统时区已设置为 ${savedValue}`)
      settingsState.refresh()
    } catch (err) {
      setTimeZoneError(err instanceof Error ? err.message : String(err))
    } finally {
      setTimeZoneSubmitting(false)
    }
  }

  return (
    <div className="page-grid one-col">
      <PageSection title="界面外观">
        <div className="form-grid compact">
          <select value={themeMode} onChange={handleThemeModeChange} aria-label="主题模式">
            <option value="system">跟随系统</option>
            <option value="light">明亮模式</option>
            <option value="dark">黑暗模式</option>
          </select>
          <div className="hint">选择“跟随系统”时，会根据操作系统的浅色/深色偏好自动切换。</div>
        </div>
      </PageSection>
      <PageSection title="系统时区">
        <StatusBanner error={settingsState.error} loading={settingsState.loading}>
          <form className="form-grid compact" onSubmit={handleTimeZoneSave}>
            <input value={timeZone} onChange={(e) => setTimeZone(e.target.value)} placeholder="Asia/Shanghai" />
            <div className="hint">定时扫描会按该时区匹配 cron；页面中的创建时间、更新时间也会按该时区展示。</div>
            {timeZoneError ? <div className="banner banner-error">{timeZoneError}</div> : null}
            {timeZoneSuccess ? <div className="banner banner-success">{timeZoneSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={timeZoneSubmitting}>{timeZoneSubmitting ? '保存中...' : '保存系统时区'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
      <PageSection title="扫描日志">
        <StatusBanner error={settingsState.error} loading={settingsState.loading}>
          <form className="form-grid compact" onSubmit={handleRetentionSave}>
            <input type="number" min="0" max="3650" step="1" value={retentionDays} onChange={(e) => setRetentionDays(e.target.value)} placeholder="保留天数" />
            <div className="hint">超过保留天数的已完成扫描任务及其日志会每小时自动删除。设置为 0 表示不自动清理。</div>
            {retentionError ? <div className="banner banner-error">{retentionError}</div> : null}
            {retentionSuccess ? <div className="banner banner-success">{retentionSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={retentionSubmitting}>{retentionSubmitting ? '保存中...' : '保存保留时间'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
      <PageSection title="系统事件">
        <StatusBanner error={settingsState.error} loading={settingsState.loading}>
          <form className="form-grid compact" onSubmit={handleEventRetentionSave}>
            <input type="number" min="0" max="3650" step="1" value={eventRetentionDays} onChange={(e) => setEventRetentionDays(e.target.value)} placeholder="保留天数" />
            <div className="hint">超过保留天数的系统事件会每小时自动删除。设置为 0 表示不自动清理。</div>
            {eventRetentionError ? <div className="banner banner-error">{eventRetentionError}</div> : null}
            {eventRetentionSuccess ? <div className="banner banner-success">{eventRetentionSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={eventRetentionSubmitting}>{eventRetentionSubmitting ? '保存中...' : '保存保留时间'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
      <PageSection title="账号安全">
        <StatusBanner error={authState.error} loading={authState.loading}>
          <form className="form-grid" onSubmit={handleAccountSave}>
            <input value={accountForm.username} onChange={(e) => setAccountForm({ ...accountForm, username: e.target.value })} placeholder="用户名" required />
            <input type="password" value={accountForm.currentPassword} onChange={(e) => setAccountForm({ ...accountForm, currentPassword: e.target.value })} placeholder="当前密码" required />
            <input type="password" value={accountForm.newPassword} onChange={(e) => setAccountForm({ ...accountForm, newPassword: e.target.value })} placeholder="新密码（可选）" />
            <input type="password" value={accountForm.confirmPassword} onChange={(e) => setAccountForm({ ...accountForm, confirmPassword: e.target.value })} placeholder="确认新密码" />
            {accountError ? <div className="banner banner-error">{accountError}</div> : null}
            {accountSuccess ? <div className="banner banner-success">{accountSuccess}</div> : null}
            <div className="button-row">
              <button type="submit" disabled={accountSubmitting}>{accountSubmitting ? '保存中...' : '更新账号'}</button>
            </div>
          </form>
        </StatusBanner>
      </PageSection>
    </div>
  )
}
