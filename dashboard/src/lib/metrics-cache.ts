"use client";

import type { MachineMetrics } from "./demo-data";

/* ============================================================================
 * Metrics cache
 *
 * Persists the last-known fleet snapshot to localStorage so that:
 *   1. On page reload, cards render instantly with previous data
 *      (no flash, no "awaiting first metrics" placeholder)
 *   2. On SSE disconnect, in-memory state survives
 *
 * Writes are debounced to avoid hammering localStorage on every metrics
 * event. The cache is keyed by user ID (falls back to "anon") so multiple
 * users on the same browser don't see each other's cached data.
 *
 * Schema version: bump CACHE_VERSION when the MachineMetrics shape changes
 * incompatibly. Stale-version caches are silently dropped on hydration.
 * ============================================================================ */

const CACHE_KEY_PREFIX = "bloxos-fleet-cache-v";
const CACHE_VERSION = 1;
const WRITE_DEBOUNCE_MS = 2000;
const MAX_CACHE_AGE_MS = 7 * 24 * 60 * 60 * 1000; // 7 days

interface CacheEnvelope {
  version: number;
  written_at: number;
  machines: MachineMetrics[];
}

function cacheKey(userID: string | null): string {
  const id = userID ?? "anon";
  return `${CACHE_KEY_PREFIX}${CACHE_VERSION}-${id}`;
}

/**
 * Read the cache for a given user. Returns null if missing, malformed,
 * or older than MAX_CACHE_AGE_MS.
 */
export function readCache(userID: string | null): MachineMetrics[] | null {
  if (typeof window === "undefined") return null;

  try {
    const raw = localStorage.getItem(cacheKey(userID));
    if (!raw) return null;

    const env = JSON.parse(raw) as CacheEnvelope;
    if (env.version !== CACHE_VERSION) {
      localStorage.removeItem(cacheKey(userID));
      return null;
    }
    if (Date.now() - env.written_at > MAX_CACHE_AGE_MS) {
      localStorage.removeItem(cacheKey(userID));
      return null;
    }
    if (!Array.isArray(env.machines)) return null;

    return env.machines;
  } catch {
    return null;
  }
}

function writeCache(userID: string | null, machines: MachineMetrics[]): void {
  if (typeof window === "undefined") return;

  try {
    const envelope: CacheEnvelope = {
      version: CACHE_VERSION,
      written_at: Date.now(),
      machines,
    };
    localStorage.setItem(cacheKey(userID), JSON.stringify(envelope));
  } catch (err) {
    if (err instanceof DOMException && err.name === "QuotaExceededError") {
      console.warn("metrics-cache: localStorage quota exceeded, skipping write");
    }
  }
}

/**
 * Clear the cache for a given user. Called on logout to avoid leaking
 * machine data between users on the same browser.
 */
export function clearCache(userID: string | null): void {
  if (typeof window === "undefined") return;
  try {
    localStorage.removeItem(cacheKey(userID));
  } catch {
    // ignore
  }
}

/**
 * Create a debounced writer. The actual localStorage write happens at
 * most once every WRITE_DEBOUNCE_MS milliseconds, with the most recent
 * value winning. The returned flush() forces an immediate write.
 */
export function makeDebouncedWriter(
  userID: string | null
): [writer: (machines: MachineMetrics[]) => void, flush: () => void] {
  let pendingMachines: MachineMetrics[] | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const flush = () => {
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
    if (pendingMachines) {
      writeCache(userID, pendingMachines);
      pendingMachines = null;
    }
  };

  const writer = (machines: MachineMetrics[]) => {
    pendingMachines = machines;
    if (timer) return;
    timer = setTimeout(() => {
      timer = null;
      if (pendingMachines) {
        writeCache(userID, pendingMachines);
        pendingMachines = null;
      }
    }, WRITE_DEBOUNCE_MS);
  };

  return [writer, flush];
}
