"use client";

// Monoform — the left half of the fleet posture row.
//
// Deliberately spare: a label, the connected count, one truthful sentence,
// and three state rows. No ring, no radial gauge, no decorative progress
// fill, no repeated metric cards — the numbers and the whitespace carry the
// hierarchy. Every state row shows a dot AND its name AND its count, so the
// meaning never depends on colour alone.

import type { ReactNode } from "react";

export interface AvailabilityCounts {
  total: number;
  /** Not offline — includes stale, warning and critical. */
  connected: number;
  live: number;
  warning: number;
  critical: number;
  stale: number;
  offline: number;
  /** warning + critical + stale. live + needsReview + offline === total. */
  needsReview: number;
}

/**
 * One sentence describing what is actually true right now. Never claims
 * "all reporting" while anything is stale, and never invents a zero.
 */
export function availabilitySummary(c: AvailabilityCounts): string {
  if (c.total === 0) return "No machines are reporting yet.";

  const issues: string[] = [];
  if (c.critical > 0) issues.push(`${c.critical} critical`);
  if (c.warning > 0) issues.push(`${c.warning} in warning`);
  if (c.stale > 0) issues.push(`${c.stale} stale`);
  if (c.offline > 0) issues.push(`${c.offline} offline`);

  if (issues.length === 0) {
    return c.total === 1
      ? "The one registered machine is connected and reporting fresh metrics."
      : `All ${c.total} machines are connected and reporting fresh metrics.`;
  }

  const list =
    issues.length === 1
      ? issues[0]
      : `${issues.slice(0, -1).join(", ")} and ${issues[issues.length - 1]}`;
  const head = list.charAt(0).toUpperCase() + list.slice(1);
  return c.live > 0
    ? `${head}. The other ${c.live} ${c.live === 1 ? "machine is" : "machines are"} reporting normally.`
    : `${head}.`;
}

export function FleetAvailabilityPanel({ counts }: { counts: AvailabilityCounts }) {
  // The "needs review" bucket takes its colour from the worst state inside it,
  // so an amber dot never stands in for a critical machine.
  const reviewTone = counts.critical > 0 ? "critical" : counts.warning > 0 ? "warning" : "stale";

  return (
    <section className="mf-panel px-7 py-6" aria-label="Fleet availability">
      <div className="mf-kicker">Fleet availability</div>

      <div className="mt-5 flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="mf-metric text-[42px] font-medium leading-none text-text-primary">
          {counts.connected}
          <span className="text-text-disabled"> / </span>
          <span className="text-text-tertiary">{counts.total}</span>
        </span>
        <span className="mf-kicker">connected</span>
      </div>

      <p className="mt-4 max-w-[52ch] text-[13px] leading-[1.55] text-text-secondary">
        {availabilitySummary(counts)}
      </p>

      <dl className="mt-7 border-t border-border-subtle">
        <StateRow tone="live" label="Online" count={counts.live} />
        <StateRow tone={reviewTone} label="Needs review" count={counts.needsReview} />
        <StateRow tone="offline" label="Offline" count={counts.offline} />
      </dl>
    </section>
  );
}

function StateRow({
  tone,
  label,
  count,
}: {
  tone: "live" | "warning" | "critical" | "stale" | "offline";
  label: ReactNode;
  count: number;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-border-subtle py-3.5 last:border-b-0">
      <span className={`mf-status-dot mf-status-${tone}`} aria-hidden="true" />
      <dt className="text-[13px] text-text-secondary">{label}</dt>
      <dd className="mf-metric ml-auto text-[15px] text-text-primary">{count}</dd>
    </div>
  );
}
