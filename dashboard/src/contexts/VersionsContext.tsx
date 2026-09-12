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
  /** OS the hub believes this agent runs on ("linux"/"windows"). Older hubs omit it. */
  os?: string;
  /** CPU architecture in GOARCH spelling; empty/omitted means it is not known. */
  arch?: string;
  /** Whether arch came from the agent itself (vs inferred from metrics). */
  arch_reported?: boolean;
}

export interface AgentBinaryInfo {
  path: string;
  source: string;
  sha: string;
  mtime: string;
  error: string;
  /** Release number embedded in the binary's marker; 0 = legacy/unnumbered.
   *  Older hubs omit the field entirely (unknown — not legacy). Presence of
   *  the marker is not proof the bytes are signed or verified. */
  release?: number;
}

export interface AgentRolloutStatus {
  platform: string;
  generation: number;
  candidate_sha: string;
  stage: number;
  status: string;
  halt_reason?: string;
  summary: string;
  /** Machines this generation has SEEN running the candidate, whatever
   * happened to their validation afterwards. */
  updated: number;
  /** The subset that completed its dwell. */
  validated: number;
  /** Reserved or offered: not yet known to be running it. */
  pending: number;
  /** Ineligible, with reasons. Not failures, and they do not halt. */
  withheld: number;
  withheld_reasons?: Record<string, string>;
  failed: number;
  failed_reasons?: Record<string, string>;
}

export interface VersionsResponse {
  /**
   * Which resolution policy produced the served binaries:
   *  - "auto"     a managed bundle shipped with the hub is in use
   *  - "legacy"   no bundle present (a source build); system paths apply
   *  - "external" the operator manages agent binaries themselves
   *  - "unusable" the delivery configuration itself is broken
   * Older hubs omit it. Each binary's own `source` says where it actually
   * resolved from; this says which policy was in force, and the two are
   * deliberately not derived from one another.
   */
  agent_delivery?: string;
  agent_delivery_error?: string;
  /**
   * Staged rollout state per platform ("linux/amd64", …), or a single
   * `unavailable` key when the controller could not be built.
   *
   * `status` is durable: "active" means the automatic policy is enabled, NOT
   * that something is being sent right now. "halted" needs a person.
   * `summary` is derived for display — whether a fleet is caught up is a
   * statement about machines that happen to be connected, so it is never
   * stored. Older hubs omit the whole field.
   */
  agent_rollout?: Record<string, AgentRolloutStatus | { unavailable?: string; status?: string; reason?: string }>;
  signing_enabled: boolean;
  signing_disabled_reason: string;
  hub_sha: string;
  hub_short_sha: string;
  hub_mtime: string;
  hub_windows_sha: string;
  hub_windows_short_sha: string;
  hub_windows_mtime: string;
  agent_binaries?: {
    linux: AgentBinaryInfo;
    windows: AgentBinaryInfo;
  };
  /**
   * Per-(os, arch) served-binary state: linux has amd64 and arm64, windows
   * has amd64. Present on per-architecture hubs; older hubs omit it, in
   * which case agent_binaries carries the legacy, architecture-unspecified states.
   */
  agent_binaries_by_arch?: Record<string, Record<string, AgentBinaryInfo>>;
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
