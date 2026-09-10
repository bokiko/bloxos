"use client";

// Monoform — the right half of the fleet posture row.
//
// The same classification the NeedsAttention stripe used (classifyMachine /
// isProblem / STATUS_ORDER), rendered as a panel instead of a chip rail:
// severity first, then most-recently-heard-from. Each row carries a 2px
// semantic rail, the machine, the real reason it is listed, its heartbeat
// age, and a direct link into the machine.
//
// Acknowledgment is NOT handled here. The alerts button opens the existing
// AlertPanel, which still owns onAcknowledge / onAcknowledgeAll unchanged.

import { useMemo } from "react";
import Link from "next/link";
import { ArrowUpRight, Bell } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import {
  classifyMachine,
  isProblem,
  STATUS_ORDER,
  STATUS_VIS,
  type MachineStatus,
} from "@/components/StatusBadge";
import { statusVar, timeSince } from "@/components/fleet/fleetModel";

interface AttentionPanelProps {
  machines: MachineMetrics[];
  /** Active alert count from SSE — the badge only, never a fabricated list. */
  alertsCount: number;
  onOpenAlerts: () => void;
  /** Shared clock tick; classifyMachine reads the wall clock, so this is what
   * re-derives stale/offline when the SSE stream goes quiet. */
  now: number;
}

interface ProblemRow {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
}

export function MonoformAttentionPanel({
  machines,
  alertsCount,
  onOpenAlerts,
  now,
}: AttentionPanelProps) {
  const problems = useMemo<ProblemRow[]>(() => {
    const out: ProblemRow[] = [];
    for (const machine of machines) {
      const { status, reason } = classifyMachine(machine);
      if (isProblem(status)) out.push({ machine, status, reason });
    }
    out.sort((a, b) => {
      const severity = STATUS_ORDER[a.status] - STATUS_ORDER[b.status];
      if (severity !== 0) return severity;
      // Then recency: the machine we heard from most recently comes first.
      const recency = (b.machine.last_seen ?? 0) - (a.machine.last_seen ?? 0);
      if (recency !== 0) return recency;
      return (a.machine.hostname ?? "").localeCompare(b.machine.hostname ?? "");
    });
    return out;
    // `now` is the deliberate time trigger — eslint cannot see the Date.now()
    // inside classifyMachine.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, now]);

  return (
    <section className="mf-panel flex min-w-0 flex-col px-7 py-6" aria-label="Needs attention">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="mf-kicker">Needs attention</div>
          <div className="mt-2 flex items-baseline gap-2">
            <span className="mf-metric text-[22px] font-medium leading-none text-text-primary">
              {problems.length}
            </span>
            <span className="text-[13px] text-text-secondary">
              {problems.length === 1 ? "machine" : "machines"}
            </span>
          </div>
        </div>
        <button
          type="button"
          onClick={onOpenAlerts}
          className="inline-flex h-9 shrink-0 items-center gap-2 rounded-[10px] border border-border-subtle px-3 text-[12px] text-text-secondary transition-colors duration-[var(--motion-fast)] hover:border-border-strong hover:text-text-primary"
          title={alertsCount > 0 ? `${alertsCount} active alerts` : "No active alerts"}
        >
          <Bell className="h-3.5 w-3.5" aria-hidden="true" />
          Alerts
          <span className="mf-metric text-text-primary">{alertsCount}</span>
        </button>
      </div>

      {problems.length === 0 ? (
        <div className="mt-6 flex items-center gap-3 border-t border-border-subtle pt-6">
          <span className="mf-status-dot mf-status-live" aria-hidden="true" />
          <p className="text-[13px] text-text-secondary">
            {machines.length === 0
              ? "No machines are reporting yet."
              : "Nothing needs attention. Every machine is within nominal thresholds."}
          </p>
        </div>
      ) : (
        <ul className="mt-5 max-h-[352px] overflow-y-auto border-t border-border-subtle">
          {problems.map(({ machine, status, reason }) => (
            <AttentionRow
              key={machine.machine_id}
              machine={machine}
              status={status}
              reason={reason}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function AttentionRow({
  machine,
  status,
  reason,
}: {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
}) {
  const vis = STATUS_VIS[status];
  // An offline machine has no live metric to blame, so its "issue" is how long
  // it has been silent.
  const detail =
    status === "offline"
      ? machine.last_seen
        ? `No heartbeat since ${timeSince(machine.last_seen)}`
        : "Never reported"
      : (reason ?? vis.label);

  return (
    <li className="relative border-b border-border-subtle last:border-b-0">
      <span
        className="absolute inset-y-2 left-0 w-[2px]"
        style={{ background: statusVar(status) }}
        aria-hidden="true"
      />
      <div className="flex items-center gap-3 py-3 pl-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={`mf-status-dot mf-status-${status}`} aria-hidden="true" />
            <span className="truncate text-[13px] font-medium text-text-primary">
              {machine.hostname || machine.machine_id}
            </span>
            <span className={`mf-metric text-[10px] uppercase tracking-[0.06em] ${vis.textClass}`}>
              {vis.label}
            </span>
          </div>
          <p className="mt-1 truncate pl-[18px] text-[12px] text-text-tertiary">{detail}</p>
        </div>
        {machine.last_seen && status !== "offline" && (
          <span className="mf-metric hidden shrink-0 text-[11px] text-text-tertiary sm:inline">
            {timeSince(machine.last_seen)}
          </span>
        )}
        <Link
          href={`/machine/${machine.machine_id}`}
          className="inline-flex shrink-0 items-center gap-1 rounded-[10px] border border-border-subtle px-2.5 py-1 text-[12px] text-accent transition-colors duration-[var(--motion-fast)] hover:border-border-strong"
        >
          Review
          <ArrowUpRight className="h-3 w-3" aria-hidden="true" />
        </Link>
      </div>
    </li>
  );
}
