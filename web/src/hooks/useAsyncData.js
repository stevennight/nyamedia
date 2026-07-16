import { useCallback, useEffect, useRef, useState } from 'react'

export function useAsyncData(loader, deps = [], options = {}) {
  const initialData = options.initialData ?? null
  const [data, setData] = useState(initialData)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(initialData === null)
  const skippedInitialRefresh = useRef(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const next = await loader()
      setData(next)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, deps)

  useEffect(() => {
    if (options.skipInitialRefresh && initialData !== null && !skippedInitialRefresh.current) {
      skippedInitialRefresh.current = true
      return
    }
    refresh()
  }, [refresh])

  return { data, error, loading, refresh, setData }
}
