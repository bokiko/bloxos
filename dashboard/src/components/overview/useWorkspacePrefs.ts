"use client";

// The Overview's own per-user view state — collapsed sections, the metric the
// load ranking is sorted by, and the machines marked "expected high load".
//
// This is deliberately NOT part of PreferencesContext. That context mirrors
// the hub's user-preferences bundle, whose scalar fields are named columns on
// `users` (hub/preferences.go); adding to it means a migration and a hub
// release. lib/workspace-prefs.mjs explains the trade-off. Everything here is
// browser-local and per user id, so a logged-out or switched user starts
// clean rather than inheriting somebody else's workspace.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useAuth } from "@/contexts/AuthContext";
import { getStoredToken } from "@/lib/session";
import { userIDFromToken } from "@/lib/auth-session.mjs";
import {
  readWorkspacePrefs,
  writeWorkspacePrefs,
  toggleMember,
  LOAD_METRICS,
} from "@/lib/workspace-prefs.mjs";

export type LoadMetric = "cpu" | "gpu" | "memory";

export interface WorkspacePrefs {
  collapsed: string[];
  load_metric: LoadMetric;
  expected_high_cpu: string[];
}

export interface WorkspaceState {
  isCollapsed: (sectionID: string) => boolean;
  toggleSection: (sectionID: string) => void;
  loadMetric: LoadMetric;
  setLoadMetric: (metric: LoadMetric) => void;
  /** Machine ids whose CPU saturation is their working state. */
  baselines: ReadonlySet<string>;
  toggleBaseline: (machineID: string) => void;
}

function isLoadMetric(value: string): value is LoadMetric {
  return LOAD_METRICS.includes(value);
}

export function useWorkspacePrefs(): WorkspaceState {
  const { token } = useAuth();
  const userID = useMemo(() => userIDFromToken(token), [token]);

  // Lazy initializer, same shape as PreferencesContext: read this user's entry
  // synchronously so a reload paints the saved collapse state on the first
  // frame instead of expanding everything and then snapping shut.
  const [prefs, setPrefs] = useState<WorkspacePrefs>(
    () => readWorkspacePrefs(userIDFromToken(getStoredToken())) as WorkspacePrefs,
  );

  // The write target must follow the token, not lag a render behind it, so it
  // is a ref rather than a dependency of every writer.
  const userIDRef = useRef<string | null>(userID);

  useEffect(() => {
    if (userIDRef.current === userID) return;
    userIDRef.current = userID;
    setPrefs(readWorkspacePrefs(userID) as WorkspacePrefs);
  }, [userID]);

  // Functional form throughout: two toggles fired in the same tick (a click
  // plus a keyboard repeat) must both land.
  const update = useCallback((patch: (prev: WorkspacePrefs) => Partial<WorkspacePrefs>) => {
    setPrefs((prev) => {
      const next = { ...prev, ...patch(prev) };
      writeWorkspacePrefs(userIDRef.current, next);
      return next;
    });
  }, []);

  const collapsed = useMemo(() => new Set(prefs.collapsed), [prefs.collapsed]);
  const baselines = useMemo(() => new Set(prefs.expected_high_cpu), [prefs.expected_high_cpu]);

  const isCollapsed = useCallback((sectionID: string) => collapsed.has(sectionID), [collapsed]);

  const toggleSection = useCallback(
    (sectionID: string) =>
      update((prev) => ({ collapsed: toggleMember(prev.collapsed, sectionID) })),
    [update],
  );

  const setLoadMetric = useCallback(
    (metric: LoadMetric) => update(() => ({ load_metric: metric })),
    [update],
  );

  const toggleBaseline = useCallback(
    (machineID: string) =>
      update((prev) => ({ expected_high_cpu: toggleMember(prev.expected_high_cpu, machineID) })),
    [update],
  );

  return {
    isCollapsed,
    toggleSection,
    loadMetric: isLoadMetric(prefs.load_metric) ? prefs.load_metric : "cpu",
    setLoadMetric,
    baselines,
    toggleBaseline,
  };
}
