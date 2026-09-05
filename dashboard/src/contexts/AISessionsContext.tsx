"use client";

// AI Sessions — the single client-side store for live AI coding sessions.
//
// Truth comes from GET /api/ai-sessions on every SSE (re)connect; deltas
// arrive over the shared SSE stream (ai_sessions / ai_sessions_removed /
// ai_sessions_config) through SSEProvider.subscribe. There is no polling
// and no second EventSource. Everything held here is the sanitized
// contract from proto/aisessions — nothing else exists to hold.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useAuth } from "./AuthContext";
import { useSSE } from "./SSEContext";
import { HUB_URL } from "@/lib/session";
import {
  type AISessionsMachine,
  type AISessionsMachineView,
  type AISessionsResponse,
  toLocalReceivedAt,
} from "@/lib/ai-sessions";

const DEFAULT_STALE_AFTER_SECONDS = 90;

interface AISessionsContextValue {
  /** null until the first bootstrap has answered. */
  enabled: boolean | null;
  /** True once GET has answered at least once (success or failure). */
  hasLoaded: boolean;
  loading: boolean;
  error: string | null;
  staleAfterSeconds: number;
  machines: Map<string, AISessionsMachine>;
  getMachine: (machineId: string) => AISessionsMachine | undefined;
  refresh: () => Promise<void>;
  /** Admin only (fleet.admin). Persists the switch and returns the new state. */
  setEnabled: (enabled: boolean) => Promise<void>;
}

const AISessionsContext = createContext<AISessionsContextValue | null>(null);

export function useAISessions() {
  const ctx = useContext(AISessionsContext);
  if (!ctx) throw new Error("useAISessions must be used within AISessionsProvider");
  return ctx;
}

function viewToMachine(view: AISessionsMachineView, hubNowMs: number, localNowMs: number): AISessionsMachine {
  return {
    machineId: view.machine_id,
    hostname: view.hostname || "",
    sessions: Array.isArray(view.sessions) ? view.sessions : [],
    receivedAtLocal: toLocalReceivedAt(view.received_at, hubNowMs, localNowMs),
  };
}

export function AISessionsProvider({ children }: { children: ReactNode }) {
  const { authFetch, isAuthenticated } = useAuth();
  const { subscribe, connected } = useSSE();
  const [enabled, setEnabledState] = useState<boolean | null>(null);
  const [hasLoaded, setHasLoaded] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [staleAfterSeconds, setStaleAfterSeconds] = useState(DEFAULT_STALE_AFTER_SECONDS);
  const [machines, setMachines] = useState<Map<string, AISessionsMachine>>(new Map());
  const mountedRef = useRef(true);
  // Serialize overlapping bootstraps: only the newest response may win.
  const refreshSeqRef = useRef(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    if (!isAuthenticated) return;
    const seq = ++refreshSeqRef.current;
    setLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/ai-sessions`);
      if (!mountedRef.current || seq !== refreshSeqRef.current) return;
      if (!res.ok) {
        setError(`Hub returned ${res.status}`);
        return;
      }
      const json: AISessionsResponse = await res.json();
      if (!mountedRef.current || seq !== refreshSeqRef.current) return;
      const localNow = Date.now();
      const hubNow = json.now ? Date.parse(json.now) : localNow;
      const next = new Map<string, AISessionsMachine>();
      if (json.enabled) {
        for (const view of json.machines ?? []) {
          if (!view.machine_id) continue;
          next.set(view.machine_id, viewToMachine(view, hubNow, localNow));
        }
      }
      setEnabledState(Boolean(json.enabled));
      if (typeof json.stale_after_seconds === "number" && json.stale_after_seconds > 0) {
        setStaleAfterSeconds(json.stale_after_seconds);
      }
      setMachines(next);
      setError(null);
    } catch (err) {
      if (!mountedRef.current || seq !== refreshSeqRef.current) return;
      setError(err instanceof Error ? err.message : "Cannot reach hub");
    } finally {
      if (mountedRef.current && seq === refreshSeqRef.current) {
        setLoading(false);
        setHasLoaded(true);
      }
    }
  }, [authFetch, isAuthenticated]);

  // Bootstrap on auth, and re-bootstrap on every SSE (re)connect: events
  // missed while disconnected are not replayed, so GET is the truth again.
  useEffect(() => {
    // Deferred to the next tick (as VersionsContext does) so React 19's
    // set-state-in-effect rule is not tripped by a synchronous reset.
    const t = setTimeout(() => {
      if (!isAuthenticated) {
        // Logout: nothing of this user's may linger for the next one.
        refreshSeqRef.current++;
        setMachines(new Map());
        setEnabledState(null);
        setHasLoaded(false);
        setError(null);
        return;
      }
      void refresh();
    }, 0);
    return () => clearTimeout(t);
  }, [isAuthenticated, connected, refresh]);

  // Live deltas.
  useEffect(() => {
    if (!isAuthenticated) return;
    const offSnapshot = subscribe("ai_sessions", (data) => {
      try {
        const view = JSON.parse(data) as AISessionsMachineView;
        if (!view || typeof view.machine_id !== "string" || !view.machine_id) return;
        const localNow = Date.now();
        setMachines((prev) => {
          const next = new Map(prev);
          // A freshly received event is, by construction, "now" on the hub.
          next.set(view.machine_id, viewToMachine(view, localNow, localNow));
          return next;
        });
        // Anything arriving means the feature is on.
        setEnabledState((e) => (e === false ? true : e ?? true));
      } catch {
        // ignore malformed frames
      }
    });
    const offRemoved = subscribe("ai_sessions_removed", (data) => {
      try {
        const msg = JSON.parse(data) as { machine_id?: string };
        if (!msg?.machine_id) return;
        setMachines((prev) => {
          if (!prev.has(msg.machine_id!)) return prev;
          const next = new Map(prev);
          next.delete(msg.machine_id!);
          return next;
        });
      } catch {
        // ignore
      }
    });
    const offConfig = subscribe("ai_sessions_config", (data) => {
      try {
        const msg = JSON.parse(data) as { enabled?: boolean };
        if (typeof msg?.enabled !== "boolean") return;
        setEnabledState(msg.enabled);
        if (!msg.enabled) setMachines(new Map());
      } catch {
        // ignore
      }
    });
    return () => {
      offSnapshot();
      offRemoved();
      offConfig();
    };
  }, [isAuthenticated, subscribe]);

  const setEnabled = useCallback(
    async (next: boolean) => {
      const res = await authFetch(`${HUB_URL}/api/ai-sessions/settings`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: next }),
      });
      if (!res.ok) {
        const json = await res.json().catch(() => null);
        throw new Error(json?.error || `Failed to update AI Sessions (${res.status})`);
      }
      const json = (await res.json().catch(() => null)) as { enabled?: boolean } | null;
      const applied = typeof json?.enabled === "boolean" ? json.enabled : next;
      setEnabledState(applied);
      if (!applied) setMachines(new Map());
    },
    [authFetch],
  );

  const getMachine = useCallback((machineId: string) => machines.get(machineId), [machines]);

  const value = useMemo<AISessionsContextValue>(
    () => ({
      enabled,
      hasLoaded,
      loading,
      error,
      staleAfterSeconds,
      machines,
      getMachine,
      refresh,
      setEnabled,
    }),
    [enabled, hasLoaded, loading, error, staleAfterSeconds, machines, getMachine, refresh, setEnabled],
  );

  return <AISessionsContext.Provider value={value}>{children}</AISessionsContext.Provider>;
}

/**
 * Coarse clock for durations and staleness. Ticks only while mounted, so a
 * page without sessions on screen costs nothing.
 */
export function useNow(intervalMs = 10_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
