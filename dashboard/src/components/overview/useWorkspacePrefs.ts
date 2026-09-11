"use client";

// The Overview's own per-user view state — the fleet power window and tariff,
// and the machines marked "expected high load".
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
  normalizePowerRate,
  toggleMember,
  POWER_PERIODS,
} from "@/lib/workspace-prefs.mjs";

export type PowerPeriod = "30m" | "1h" | "6h" | "24h";

/** The electricity tariff the fleet power pane costs energy at. A null rate
 * is "not stated" — there is deliberately no default. */
export interface PowerRate {
  currency: string;
  per_kwh: number | null;
}

export interface WorkspacePrefs {
  expected_high_cpu: string[];
  power_period: PowerPeriod;
  power_rate: PowerRate;
}

export interface WorkspaceState {
  /** Machine ids whose CPU saturation is their working state. */
  baselines: ReadonlySet<string>;
  toggleBaseline: (machineID: string) => void;
  powerPeriod: PowerPeriod;
  setPowerPeriod: (period: PowerPeriod) => void;
  powerRate: PowerRate;
  setPowerRate: (rate: PowerRate) => void;
}

function isPowerPeriod(value: string): value is PowerPeriod {
  return POWER_PERIODS.includes(value);
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

  const baselines = useMemo(() => new Set(prefs.expected_high_cpu), [prefs.expected_high_cpu]);



  const toggleBaseline = useCallback(
    (machineID: string) =>
      update((prev) => ({ expected_high_cpu: toggleMember(prev.expected_high_cpu, machineID) })),
    [update],
  );

  const setPowerPeriod = useCallback(
    (period: PowerPeriod) => update(() => ({ power_period: period })),
    [update],
  );

  // Normalised on the way in as well as on the way out: the rate comes from a
  // free-text field, and an unusable one must land as "not stated" rather than
  // as a zero tariff that would print a confident cost of nothing.
  const setPowerRate = useCallback(
    (rate: PowerRate) => update(() => ({ power_rate: normalizePowerRate(rate) as PowerRate })),
    [update],
  );

  const powerRate = useMemo(() => normalizePowerRate(prefs.power_rate) as PowerRate, [prefs.power_rate]);

  return {
    baselines,
    toggleBaseline,
    powerPeriod: isPowerPeriod(prefs.power_period) ? prefs.power_period : "6h",
    setPowerPeriod,
    powerRate,
    setPowerRate,
  };
}
