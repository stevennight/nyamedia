import { useCallback, useEffect, useRef, useState } from 'react'

export function useAsyncData(loader, deps = [], options = {}) {
  const initialData = options.initialData ?? null
  const [data, setData] = useState(initialData)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(initialData === null)
  const skippedInitialRefresh = useRef(false)
  const mounted = useRef(false)
  const requestSequence = useRef(0)
  const activeRequest = useRef(null)

  const refresh = useCallback(async () => {
    const requestId = ++requestSequence.current
    activeRequest.current?.controller.abort()

    const controller = new AbortController()
    activeRequest.current = { controller, requestId }
    if (mounted.current) {
      setLoading(true)
      setError('')
    }

    try {
      const next = await loader(controller.signal)
      if (mounted.current && activeRequest.current?.requestId === requestId && !controller.signal.aborted) {
        setData(next)
      }
      return next
    } catch (err) {
      if (controller.signal.aborted || err?.name === 'AbortError') {
        return undefined
      }
      if (mounted.current && activeRequest.current?.requestId === requestId) {
        setError(err instanceof Error ? err.message : String(err))
      }
      return undefined
    } finally {
      if (activeRequest.current?.requestId === requestId) {
        activeRequest.current = null
        if (mounted.current) {
          setLoading(false)
        }
      }
    }
  }, deps)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      activeRequest.current?.controller.abort()
      activeRequest.current = null
    }
  }, [])

  useEffect(() => {
    if (options.skipInitialRefresh && initialData !== null && !skippedInitialRefresh.current) {
      skippedInitialRefresh.current = true
      return
    }
    refresh()
    return () => {
      activeRequest.current?.controller.abort()
    }
  }, [refresh])

  return { data, error, loading, refresh, setData }
}
