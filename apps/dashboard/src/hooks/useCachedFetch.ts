'use client'

import { useState, useEffect, useCallback, useRef } from 'react'
import { getApiUrl } from '../lib/api'

interface CachedFetchOptions {
  ttl?: number
  credentials?: RequestCredentials
  enabled?: boolean
  revalidateOnFocus?: boolean
}

interface CacheEntry {
  data: unknown
  fetchedAt: number
}

interface UseCachedFetchResult<T> {
  data: T | null
  loading: boolean
  error: string | null
  refresh: () => void
}

const cache = new Map<string, CacheEntry>()
const inFlight = new Map<string, Promise<unknown>>()

function isFresh(entry: CacheEntry, ttl: number): boolean {
  return Date.now() - entry.fetchedAt < ttl
}

export function useCachedFetch<T>(
  url: string,
  options?: CachedFetchOptions
): UseCachedFetchResult<T> {
  const {
    ttl = 30000,
    credentials = 'include',
    enabled = true,
    revalidateOnFocus = false,
  } = options ?? {}

  const cached = cache.get(url)
  const [data, setData] = useState<T | null>(
    cached ? (cached.data as T) : null
  )
  const [loading, setLoading] = useState<boolean>(!cached && enabled)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  const doFetch = useCallback(
    async (isRevalidation: boolean) => {
      if (!enabled) return

      // Deduplicate in-flight requests
      let promise = inFlight.get(url) as Promise<T> | undefined
      if (!promise) {
        promise = (async () => {
          const fullUrl = url.startsWith('http') ? url : `${getApiUrl()}${url}`
          const res = await fetch(fullUrl, { credentials })
          if (!res.ok) {
            throw new Error(`${res.status} ${res.statusText}`)
          }
          return res.json() as Promise<T>
        })()
        inFlight.set(url, promise)
      }

      if (!isRevalidation) {
        setLoading(true)
      }

      try {
        const result = await promise
        cache.set(url, { data: result, fetchedAt: Date.now() })
        if (mountedRef.current) {
          setData(result)
          setError(null)
        }
      } catch (err) {
        if (mountedRef.current) {
          setError(err instanceof Error ? err.message : 'Fetch failed')
        }
      } finally {
        inFlight.delete(url)
        if (mountedRef.current) {
          setLoading(false)
        }
      }
    },
    [url, credentials, enabled]
  )

  useEffect(() => {
    mountedRef.current = true
    if (!enabled) {
      setLoading(false)
      return
    }

    const entry = cache.get(url)
    if (entry) {
      setData(entry.data as T)
      if (isFresh(entry, ttl)) {
        setLoading(false)
        return
      }
      // Stale — return cached data, revalidate in background
      setLoading(false)
      doFetch(true)
    } else {
      doFetch(false)
    }

    return () => {
      mountedRef.current = false
    }
  }, [url, ttl, enabled, doFetch])

  useEffect(() => {
    if (!revalidateOnFocus || !enabled) return

    const onFocus = () => {
      const entry = cache.get(url)
      if (!entry || !isFresh(entry, ttl)) {
        doFetch(true)
      }
    }

    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [url, ttl, revalidateOnFocus, enabled, doFetch])

  const refresh = useCallback(() => {
    cache.delete(url)
    doFetch(false)
  }, [url, doFetch])

  return { data, loading, error, refresh }
}
