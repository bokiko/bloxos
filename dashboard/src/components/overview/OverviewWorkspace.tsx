"use client";

/* ============================================================================
 * The Overview's adaptive region: fleet power, and the compact context the
 * operator chose to keep beside it.
 *
 * This component owns the ARRANGEMENT and nothing else. It fetches nothing,
 * classifies nothing and decides nothing about the data — it is handed what
 * the page already has and lays it out. Every module is free to render
 * nothing, and the grid is sized by what will actually appear.
 *
 * That last point is why `visibleOverviewModules` lives in a pure module and
 * is called HERE rather than the obvious `[<A/>, <B/>].filter(Boolean)`:
 * React cannot tell a parent that a child returned null, so filtering over
 * elements filters nothing and a selected-but-empty module leaves a hole in
 * the grid. The parent decides what will render, `data-modules` carries that
 * count to CSS, and the minis still return null defensively.
 *
 * Two things are always true, in every arrangement:
 *   - Fleet power is visible. It is the anchor, never folded away.
 *   - Whatever the operator hides, a critical or offline machine still raises
 *     a visible marker. Power focus hides the modules; it does not hide an
 *     incident.
 * ========================================================================== */

import { ArrowRight } from "lucide-react";
import type { AlertData } from "@/lib/demo-data";
import type { OverviewLayout, OverviewWidgets } from "@/contexts/PreferencesContext";
import { visibleOverviewModules } from "@/lib/overview-layout.mjs";
import { FleetPowerPane, type PowerPeriod, type PowerRate } from "./FleetPowerPane";
import {
  FleetAvailabilityMini,
  type AvailabilityCounts,
  type MachineStateFilter,
} from "./FleetAvailabilityMini";
import { NeedsAttentionMini } from "./NeedsAttentionMini";
import { UrgentAlertMini } from "./UrgentAlertMini";

export interface OverviewWorkspaceProps {
  arrangement: OverviewLayout;
  widgets: OverviewWidgets;
  /** Fleet data has arrived at least once. Before that even zeros are a guess. */
  ready: boolean;
  counts: AvailabilityCounts;
  alerts: AlertData[];
  /** Whether the alert list could be read at all. */
  alertsStatus: "loading" | "ready" | "error";
  /** Opens the alert sheet. Only ever wired to real alert data. */
  onOpenAlerts: () => void;
  /** Filters the machine table below and moves focus to it. */
  onFilterStatus: (filter: MachineStateFilter) => void;
  powerPeriod: PowerPeriod;
  onPowerPeriodChange: (period: PowerPeriod) => void;
  powerRate: PowerRate;
}

export function OverviewWorkspace({
  arrangement,
  widgets,
  ready,
  counts,
  alerts,
  alertsStatus,
  onOpenAlerts,
  onFilterStatus,
  powerPeriod,
  onPowerPeriodChange,
  powerRate,
}: OverviewWorkspaceProps) {
  const modules = visibleOverviewModules(arrangement, widgets, { ready, alerts }) as string[];

  const render = (key: string) => {
    switch (key) {
      case "availability":
        return <FleetAvailabilityMini key={key} counts={counts} onFilterStatus={onFilterStatus} />;
      case "attention":
        return <NeedsAttentionMini key={key} alerts={alerts} status={alertsStatus} onOpenAlerts={onOpenAlerts} />;
      case "urgent_alert":
        return <UrgentAlertMini key={key} alerts={alerts} />;
      default:
        return null;
    }
  };

  const stack =
    modules.length > 0 ? (
      <aside className="mf-overview-stack" aria-label="Fleet context">
        {modules.map(render)}
      </aside>
    ) : null;

  const power = (
    <FleetPowerPane period={powerPeriod} onPeriodChange={onPowerPeriodChange} rate={powerRate} />
  );

  return (
    <section
      className="mf-overview-workspace"
      aria-label="Fleet overview"
      data-arrangement={arrangement}
      data-modules={modules.length}
    >
      {arrangement === "power-focus" && <FleetWarningStrip counts={counts} alerts={alerts} onOpenAlerts={onOpenAlerts} onFilterStatus={onFilterStatus} />}
      {/* Balanced puts the context row above the anchor. The stack is written
          BEFORE the pane in the JSX rather than reordered in CSS, so reading
          order and focus order match what is on the screen. */}
      {arrangement === "balanced" ? (
        <>
          {stack}
          {power}
        </>
      ) : (
        <>
          {power}
          {stack}
        </>
      )}
    </section>
  );
}

/**
 * Power focus hides the context modules, so this is the line that keeps it
 * honest: with a critical or offline machine, something still says so.
 *
 * Its controls lead to the data they describe, which is the whole point. The
 * counts are machine state, so the primary control filters the machine table.
 * "Open alerts" appears only when the hub has actually sent alerts — offering
 * it on a fleet with an offline machine and no alert rule firing would open an
 * empty sheet and call that an answer.
 */
function FleetWarningStrip({
  counts,
  alerts,
  onOpenAlerts,
  onFilterStatus,
}: {
  counts: AvailabilityCounts;
  alerts: AlertData[];
  onOpenAlerts: () => void;
  onFilterStatus: (filter: MachineStateFilter) => void;
}) {
  const urgent = counts.critical + counts.offline;
  if (urgent === 0) return null;

  const parts: string[] = [];
  if (counts.critical > 0) parts.push(`${counts.critical} critical`);
  if (counts.offline > 0) parts.push(`${counts.offline} offline`);
  const summary = parts.join(" · ");
  // Critical machines are in the review bucket; a fleet whose only problem is
  // an offline box goes straight to the offline filter.
  const filter: MachineStateFilter = counts.critical > 0 ? "needs-review" : "offline";

  return (
    <div className="mf-overview-warning" role="status">
      <span className="mf-status-dot mf-status-critical" aria-hidden="true" />
      <span className="mf-metric text-[13px] text-text-primary">{summary}</span>
      <div className="ml-auto flex items-center gap-2">
        <button
          type="button"
          className="mf-inline-action"
          onClick={() => onFilterStatus(filter)}
          aria-label={`Show the ${summary} in the machine table`}
        >
          Show machines
          <ArrowRight aria-hidden="true" />
        </button>
        {alerts.length > 0 && (
          <button type="button" className="mf-inline-action" onClick={onOpenAlerts}>
            Open alerts
            <ArrowRight aria-hidden="true" />
          </button>
        )}
      </div>
    </div>
  );
}
