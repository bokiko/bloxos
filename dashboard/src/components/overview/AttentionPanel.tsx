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
import { ArrowUpRight, Bell, Gauge } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import {
  isProblem,
  STATUS_ORDER,
  STATUS_VIS,
  type MachineStatus,
} from "@/components/StatusBadge";
import { classifyWith, statusVar, timeSince, type LoadBaselines } from "@/components/fleet/fleetModel";
import { Disclosure } from "./Disclosure";

interface AttentionPanelProps {
  machines: MachineMetrics[];
  /** Active alert count from SSE — the badge only, never a fabricated list. */
  alertsCount: number;
  onOpenAlerts: () => void;
  /** Shared clock tick; classifyMachine reads the wall clock, so this is what
   * re-derives stale/offline when the SSE stream goes quiet. */
  now: number;
  /** Machines whose CPU saturation is their working state. A machine listed
   * here leaves this panel when CPU was its only complaint — and stays if
   * anything else about it is wrong. */
  baselines: LoadBaselines;
  /** Set or clear that flag from this panel. This is where the flag belongs:
   * a control that decides what the dashboard escalates has to be reachable
   * from the list of things it is currently escalating. */
  onToggleBaseline: (machineID: string) => void;
  open: boolean;
  onToggle: () => void;
}

interface ProblemRow {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
  /** The CPU reading an expected-high-load flag swallowed, when it did. */
  suppressed?: string;
}

export function MonoformAttentionPanel({
  machines,
  alertsCount,
  onOpenAlerts,
  now,
  baselines,
  onToggleBaseline,
  open,
  onToggle,
}: AttentionPanelProps) {
  const problems = useMemo<ProblemRow[]>(() => {
    const out: ProblemRow[] = [];
    for (const machine of machines) {
      const { status, reason, suppressed } = classifyWith(machine, baselines);
      if (isProblem(status)) out.push({ machine, status, reason, suppressed });
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
  }, [machines, now, baselines]);

  const alertsButton = (
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
  );

  return (
    <section
      className="mf-section min-w-0"
      aria-label="Needs attention"
      data-open={open ? "true" : "false"}
    >
      {/* The alerts button is dropped from the header while the section is
          folded. A folded section is one quiet line on a hairline; a 36px
          bordered button on that line turns it straight back into a card,
          which is what folding it was meant to get rid of. Nothing becomes
          unreachable — the shell's top bar carries the same alerts button,
          with the same count badge, on every route. */}
      <Disclosure
        id="attention"
        label="Needs attention"
        open={open}
        onToggle={onToggle}
        actions={open ? alertsButton : null}
        summary={`${problems.length} ${problems.length === 1 ? "machine" : "machines"}`}
      >
        <div className="mt-2 flex items-baseline gap-2">
          <span className="mf-metric text-[22px] font-medium leading-none text-text-primary">
            {problems.length}
          </span>
          <span className="text-[13px] text-text-secondary">
            {problems.length === 1 ? "machine" : "machines"}
          </span>
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
            {problems.map(({ machine, status, reason, suppressed }) => (
              <AttentionRow
                key={machine.machine_id}
                machine={machine}
                status={status}
                reason={reason}
                suppressed={suppressed}
                baselined={baselines.has(machine.machine_id)}
                onToggleBaseline={onToggleBaseline}
              />
            ))}
          </ul>
        )}
      </Disclosure>
    </section>
  );
}

function AttentionRow({
  machine,
  status,
  reason,
  suppressed,
  baselined,
  onToggleBaseline,
}: {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
  suppressed?: string;
  baselined: boolean;
  onToggleBaseline: (machineID: string) => void;
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

  // The row is here BECAUSE of its CPU. classifyMachine's CPU reasons are the
  // only ones of the form "CPU 94%", and it is the only rule the flag can
  // suppress — so this is exactly the set of rows where "this is normal for
  // this machine" is an answer the operator can actually give.
  const cpuDriven = reason?.startsWith("CPU ") === true;
  // A machine already flagged is still listed for some OTHER reason. Offering
  // the reverse here keeps the decision undoable from the place it was made,
  // and keeps the row honest about why it is being shown.
  const offerBaseline = cpuDriven || baselined;

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
            {/* Listed for a different reason than its CPU. Say so, rather than
                let the operator wonder why the busiest box reads as a disk
                problem. */}
            {suppressed && (
              <span className="mf-suppressed" title={`Expected high load — ${suppressed} not escalated`}>
                {suppressed} expected
              </span>
            )}
          </div>
          <p className="mt-1 truncate pl-[18px] text-[12px] text-text-tertiary">{detail}</p>
          {/* The one control that changes what this list contains, on the list
              itself, in words. It used to exist only as a gauge glyph in the
              far-right column of the fleet table below. */}
          {offerBaseline && (
            <div className="mt-1.5 pl-[18px]">
              <button
                type="button"
                className="mf-inline-action"
                aria-pressed={baselined}
                onClick={() => onToggleBaseline(machine.machine_id)}
                title={
                  baselined
                    ? `Escalate high CPU on ${machine.hostname || machine.machine_id} again`
                    : `Treat high CPU on ${machine.hostname || machine.machine_id} as its normal working state`
                }
              >
                <Gauge className="h-3 w-3" aria-hidden="true" />
                {baselined ? "Alert on CPU again" : "Expected here — stop alerting"}
              </button>
            </div>
          )}
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
