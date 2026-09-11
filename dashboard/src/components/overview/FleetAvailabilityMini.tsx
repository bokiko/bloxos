"use client";

// Monoform — fleet availability, as one compact module.
//
// Two things, and nothing else: the connected count, and three state rows. No
// ring, no radial gauge, no decorative progress fill, no repeated metric
// cards — the numbers and the whitespace carry the hierarchy. Every state row
// shows a dot AND its name AND its count, so the meaning never depends on
// colour alone.
//
// It used to carry a sentence between the two ("1 in warning. The other 5
// machines are reporting normally."). The rows underneath already say that,
// per state, as numbers. What the sentence did add — the *reason* a machine is
// in the review bucket, critical vs warning vs stale — is on the row's tooltip
// and in its accessible name, where it costs no vertical space.
//
// A row at zero is drawn quiet, not in its severity colour. "Offline 0" used
// to paint a red dot on a fleet where nothing was offline, which is exactly
// backwards: red has to mean something is wrong, and a count of zero is the
// statement that nothing is. The label and the number are still there — the
// row is not removed, only de-escalated.
//
// THE ROWS THAT COUNT SOMETHING WRONG ARE CONTROLS, AND THEY LEAD TO MACHINES.
// These counts are machine STATE, classified from live readings on this
// client. They are not the hub's alert list: a machine can be stale with no
// alert rule firing, and an alert can outlive the condition that raised it.
// So "Needs review 3" filters the machine table directly below and takes you
// to it — the three machines it is counting. Sending it to the alert sheet
// would be a button promising a list that may be empty or may be about
// something else. Needs attention, which really is built from hub alerts,
// is the module that opens that sheet.
//
// A row at zero is NOT a control: there is nothing to go and look at.

import type { ReactNode } from "react";
import { ArrowRight } from "lucide-react";

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

/** The table filters these rows can send the operator to. */
export type MachineStateFilter = "needs-review" | "offline";

/**
 * What is actually inside the "Needs review" bucket, as chips rather than a
 * sentence: "2 critical · 1 warning". The bucket's dot takes the worst state
 * in it, so without this an amber dot could be standing over a critical
 * machine — the breakdown is the tooltip and the accessible name, never a
 * paragraph on the page.
 *
 * Empty string when the bucket is empty; there is nothing to name.
 */
export function reviewBreakdown(c: AvailabilityCounts): string {
  const parts: string[] = [];
  if (c.critical > 0) parts.push(`${c.critical} critical`);
  if (c.warning > 0) parts.push(`${c.warning} warning`);
  if (c.stale > 0) parts.push(`${c.stale} stale`);
  return parts.join(" · ");
}

export function FleetAvailabilityMini({
  counts,
  onFilterStatus,
}: {
  counts: AvailabilityCounts;
  /** Filters the machine table below and moves focus to it. */
  onFilterStatus: (filter: MachineStateFilter) => void;
}) {
  // The "needs review" bucket takes its colour from the worst state inside it,
  // so an amber dot never stands in for a critical machine.
  const reviewTone = counts.critical > 0 ? "critical" : counts.warning > 0 ? "warning" : "stale";

  return (
    <section className="mf-overview-module" aria-label="Fleet availability">
      <div className="mf-overview-module-head">
        <h3 className="mf-kicker">Fleet availability</h3>
      </div>

      <div className="mt-2 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
        <span className="mf-metric text-[24px] font-medium leading-none text-text-primary">
          {counts.connected}
          <span className="text-text-disabled"> / </span>
          <span className="text-text-tertiary">{counts.total}</span>
        </span>
        <span className="mf-kicker">connected</span>
      </div>

      {/* A list, not a <dl>: the two rows that count something wrong are
          buttons, and a <button> is not a valid child of a definition list.
          The dot / name / count reading order is unchanged. */}
      <ul className="mf-state-list mt-3">
        <StateRow tone="live" label="Online" count={counts.live} />
        <StateRow
          tone={reviewTone}
          label="Needs review"
          count={counts.needsReview}
          detail={reviewBreakdown(counts)}
          destination="machines that need review"
          onActivate={() => onFilterStatus("needs-review")}
        />
        <StateRow
          tone="offline"
          label="Offline"
          count={counts.offline}
          destination="offline machines"
          onActivate={() => onFilterStatus("offline")}
        />
      </ul>
    </section>
  );
}

export function StateRow({
  tone,
  label,
  count,
  detail,
  destination,
  onActivate,
}: {
  tone: "live" | "warning" | "critical" | "stale" | "offline";
  label: string;
  count: number;
  /** What is inside the bucket, for the tooltip and the accessible name. */
  detail?: string;
  /** Where activating the row goes, named in the accessible label. */
  destination?: string;
  /** Given, and with something in the bucket, the row becomes a button. */
  onActivate?: () => void;
}) {
  // Nothing in this bucket means nothing to signal: the dot and the count both
  // drop to the quiet tone so severity colour stays reserved for severity.
  const empty = count === 0;
  const actionable = !empty && onActivate !== undefined;

  const body: ReactNode = (
    <>
      <span
        className={`mf-status-dot ${empty ? "mf-status-none" : `mf-status-${tone}`}`}
        aria-hidden="true"
      />
      <span className={`text-[13px] ${empty ? "text-text-tertiary" : "text-text-secondary"}`}>
        {label}
      </span>
      <span
        className={`mf-metric ml-auto text-[15px] ${empty ? "text-text-disabled" : "text-text-primary"}`}
      >
        {count}
      </span>
      {/* Drawn at rest, not on hover: an affordance nobody can see until it
          is already under the pointer is not an affordance. The slot is kept
          on every row, arrow or not, so the counts stay in one column. */}
      <span className="mf-state-row-go" aria-hidden="true">
        {actionable && <ArrowRight />}
      </span>
    </>
  );

  const machines = `${count} ${count === 1 ? "machine" : "machines"}`;
  // The breakdown, where there is one, rides along on both the tooltip and the
  // accessible name rather than being spelled out under the rows.
  const inside = !empty && detail ? ` (${detail})` : "";
  const action = destination ? `Show ${destination}.` : "";

  return (
    <li>
      {actionable ? (
        <button
          type="button"
          className="mf-state-row"
          onClick={onActivate}
          // The visible row reads "Needs review 3". The name says what is in
          // the bucket and what the button DOES, because "3" is not a
          // destination and an amber dot is not a diagnosis.
          aria-label={`${label}: ${machines}${inside}. ${action}`.trim()}
          title={detail && !empty ? `${detail} — show them in the table` : "Show them in the table"}
        >
          {body}
        </button>
      ) : (
        <div className="mf-state-row" title={inside ? detail : undefined}>
          {body}
        </div>
      )}
    </li>
  );
}
