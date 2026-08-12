"use client";

import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  ReactNode,
} from "react";
import { useAuth } from "./AuthContext";
import { HUB_URL } from "@/lib/session";

export interface AgentVersionInfo {
  machine_id: string;
  hostname: string;
  running_sha?: string;
  reported_at: string;
  update_pending: boolean;
  update_blocked_reason?: string;
  update_key_pinned: boolean;
  update_transport_ok: boolean;
  update_protocol: number;
}

export interface VersionsResponse {
  signing_enabled: boolean;
  signing_disabled_reason: string;
  hub_sha: string;
  hub_short_sha: string;
  hub_mtime: string;
  agents: AgentVersionInfo[];
  rollout_paused: boolean;
  pause_reason: string;
}

interface VersionsContextValue {
  data: VersionsResponse | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  pauseRollout: () => Promise<void>;
  resumeRollout: () => Promise<void>;
  getMachineVersion: (machineID: string) => AgentVersionInfo | undefined;
}

const VersionsContext = createContext<VersionsContextValue | null>(null);

export function VersionsProvider({ children }: { children: ReactNode }) {
  const { authFetch, isAuthenticated } = useAuth();
  const [data, setData] = useState<VersionsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!isAuthenticated) return;
    setLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/versions`);
      if (!res.ok) {
        setError(`Hub returned ${res.status}`);
        return;
      }
      const json: VersionsResponse = await res.json();
      setData(json);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Cannot reach hub");
    } finally {
      setLoading(false);
    }
  }, [authFetch, isAuthenticated]);

  const pauseRollout = useCallback(async () => {
    if (!isAuthenticated) return;
    setError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/versions/pause`, { method: "POST" });
      if (!res.ok) {
        const json = await res.json().catch(() => null);
        throw new Error(json?.error || `Failed to pause rollout (${res.status})`);
      }
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to pause rollout");
    }
  }, [authFetch, isAuthenticated, refresh]);

  const resumeRollout = useCallback(async () => {
    if (!isAuthenticated) return;
    setError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/versions/resume`, { method: "POST" });
      if (!res.ok) {
        const json = await res.json().catch(() => null);
        throw new Error(json?.error || `Failed to resume rollout (${res.status})`);
      }
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to resume rollout");
    }
  }, [authFetch, isAuthenticated, refresh]);

  // Initial fetch + 60s poll. The initial call is deferred to the next
  // tick so React 19's set-state-in-effect rule isn't tripped by
  // refresh()'s synchronous setLoading(true).
  useEffect(() => {
    if (!isAuthenticated) return;
    const initialTimer = setTimeout(() => {
      void refresh();
    }, 0);
    const id = setInterval(refresh, 60_000);
    return () => {
      clearTimeout(initialTimer);
      clearInterval(id);
    };
  }, [isAuthenticated, refresh]);

  const getMachineVersion = useCallback(
    (machineID: string) => data?.agents.find((a) => a.machine_id === machineID),
    [data]
  );

  return (
    <VersionsContext.Provider
      value={{ data, loading, error, refresh, pauseRollout, resumeRollout, getMachineVersion }}
    >
      {children}
    </VersionsContext.Provider>
  );
}

export function useVersions(): VersionsContextValue {
  const ctx = useContext(VersionsContext);
  if (!ctx) throw new Error("useVersions must be used within VersionsProvider");
  return ctx;
}
