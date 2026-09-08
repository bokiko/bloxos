"use client";

import { useEffect, useMemo, useState } from "react";
import { useSSE } from "@/contexts/SSEContext";
import { useAISessions } from "@/contexts/AISessionsContext";
import { DEMO_MODE } from "@/lib/session";
import { demoMachines, type MachineMetrics } from "@/lib/demo-data";
import { STATUS_ORDER } from "@/components/StatusBadge";
import { aggregate, statusOf, type FleetAggregate, type Metric } from "./fleetModel";

export interface FleetData {
  machines: MachineMetrics[];
  /** Problem-first ordering (critical -> warning -> offline -> stale -> live). */
  sorted: MachineMetrics[];
  agg: FleetAggregate;
  /** Live AI session count, or null when monitoring is off/failed/not loaded. */
  sessionCount: Metric;
  sessionMachines: number;
  alertsCount: number;
  isDemo: boolean;
  hasReceivedData: boolean;
  /** No machines are known yet (waiting for telemetry). */
  isEmpty: boolean;
}

/** Single source of truth for the live layout bodies. Everything is real SSE
 * data through the existing providers; nothing is fabricated. A one-second
 * tick re-derives freshness so machines age to stale/offline even if the SSE
 * stream goes quiet, matching FleetOverview's clock. */
export function useFleetData(): FleetData {
  const { machines: live, hasReceivedData, alertCount } = useSSE();
  const ai = useAISessions();

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const isDemo = DEMO_MODE && !hasReceivedData && live.length === 0;
  const source = isDemo ? demoMachines : live;

  const machines = useMemo(
    () => source.filter((m) => m && typeof m.machine_id === "string"),
    [source],
  );

  // `now` is threaded in so freshness re-evaluates on the tick, not only when
  // the machine list changes.
  const agg = useMemo(() => aggregate(machines, now), [machines, now]);

  const sorted = useMemo(
    () =>
      [...machines].sort(
        (a, b) =>
          STATUS_ORDER[statusOf(a)] - STATUS_ORDER[statusOf(b)] ||
          (a.hostname ?? "").localeCompare(b.hostname ?? ""),
      ),
    // `now` re-sorts as machines age even when SSE is quiet; statusOf reads the
    // wall clock internally, so it is a deliberate extra dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [machines, now],
  );

  const { sessionCount, sessionMachines } = useMemo(() => {
    // AI Sessions is metadata-only and can be switched off fleet-wide. When it
    // is off, has not loaded, or the last fetch errored, the count is
    // unavailable — never reported as zero active sessions. Per-machine
    // freshness (receivedAtLocal vs staleAfterSeconds) means a machine whose
    // reports have gone stale does not keep claiming active sessions.
    if (ai.enabled !== true || !ai.hasLoaded || ai.error) {
      return { sessionCount: null as Metric, sessionMachines: 0 };
    }
    const staleMs = Math.max(0, ai.staleAfterSeconds) * 1000;
    let count = 0;
    let withSessions = 0;
    for (const m of ai.machines.values()) {
      const fresh = Number.isFinite(m.receivedAtLocal) && now - m.receivedAtLocal <= staleMs;
      if (fresh && m.sessions.length > 0) {
        count += m.sessions.length;
        withSessions += 1;
      }
    }
    return { sessionCount: count as Metric, sessionMachines: withSessions };
  }, [ai.enabled, ai.hasLoaded, ai.error, ai.staleAfterSeconds, ai.machines, now]);

  return {
    machines,
    sorted,
    agg,
    sessionCount,
    sessionMachines,
    // The SSE alert count is maintained independently and can arrive before or
    // during a failed GET /api/alerts, so it is the reliable source — not the
    // length of the alerts array, which is empty until that fetch succeeds.
    alertsCount: alertCount,
    isDemo,
    hasReceivedData,
    isEmpty: machines.length === 0,
  };
}
