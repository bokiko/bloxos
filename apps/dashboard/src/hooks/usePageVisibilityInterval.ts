'use client'

import { useEffect, useRef, useState } from 'react'

/**
 * Calls `callback` on a recurring interval, but automatically pauses
 * when the browser tab is hidden (Page Visibility API) and resumes —
 * with an immediate call — when it becomes visible again.
 *
 * Returns `isActive`: true when the interval is currently running.
 */
export function usePageVisibilityInterval(
  callback: () => void | Promise<void>,
  intervalMs: number,
  enabled: boolean = true
): boolean {
  const callbackRef = useRef(callback)
  callbackRef.current = callback

  const [isActive, setIsActive] = useState(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    if (!enabled) {
      setIsActive(false)
      return
    }

    let running = true

    function scheduleNext() {
      if (!running || !mountedRef.current) return
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
        setIsActive(false)
        return
      }
      setIsActive(true)
      timerRef.current = setTimeout(async () => {
        if (!running || !mountedRef.current) return
        await Promise.resolve(callbackRef.current())
        if (running && mountedRef.current) scheduleNext()
      }, intervalMs)
    }

    scheduleNext()

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') {
        // Immediate call then resume schedule
        Promise.resolve(callbackRef.current()).then(() => {
          if (running && mountedRef.current) scheduleNext()
        })
      } else {
        if (timerRef.current) {
          clearTimeout(timerRef.current)
          timerRef.current = null
        }
        setIsActive(false)
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', onVisibilityChange)
    }

    return () => {
      running = false
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', onVisibilityChange)
      }
      setIsActive(false)
    }
  }, [enabled, intervalMs])

  return isActive
}
