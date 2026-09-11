"use client";

// Monoform — the left half of the fleet posture row.
//
// Two things, and nothing else: the connected count, and three state rows. No
// ring, no radial gauge, no decorative progress fill, no repeated metric
// cards — the numbers and the whitespace carry the hierarchy. Every state row
// shows a dot AND its name AND its count, so the meaning never depends on
// colour alone.
//
// It used to carry a sentence between the two ("1 in warning. The other 5
// machines are reporting normally."). The rows underneath already say that,
// per state, as numbers; the sentence was the same fact spelled out in prose
// and it is gone. What it did add — the *reason* a machine is in the review
// bucket, critical vs warning vs stale — is on the row's tooltip and in its
// accessible name, where it costs no vertical space.
//
// A row at zero is drawn quiet, not in its severity colour. "Offline 0" used
// to paint a red dot on a fleet where nothing was offline, which is exactly
// backwards: red has to mean something is wrong, and a count of zero is the
// statement that nothing is. The label and the number are still there — the
// row is not removed, only de-escalated.
//
// THE ROWS THAT COUNT SOMETHING WRONG ARE CONTROLS.
// This pane is now the Overview's only summary of fleet posture — the
// attention panel that used to sit beside it is gone — so "Needs review 3"
// must be a way in, not a dead end. Those two rows are real <button>s that
// open the alerts panel (the same sheet the shell's bell opens, with the same
// acknowledge actions). A row at zero is NOT a control: there is nothing to
// go and look at, and a button that opens an empty sheet is a lie about
// there being something behind it.

import type { ReactNode } from "react";
import { ArrowRight } from "lucide-react";
import { Disclosure } from "./Disclosure";

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

export function FleetAvailabilityPanel({
  counts,
  onOpenAlerts,
  open,
  onToggle,
}: {
  counts: AvailabilityCounts;
  /** Opens the alerts sheet. Wired to the same handler as the shell's bell. */
  onOpenAlerts: () => void;
  open: boolean;
  onToggle: () => void;
}) {
  // The "needs review" bucket takes its colour from the worst state inside it,
  // so an amber dot never stands in for a critical machine.
  const reviewTone = counts.critical > 0 ? "critical" : counts.warning > 0 ? "warning" : "stale";

  return (
    // `mf-pane-fill`: this pane holds four numbers now, and its partner holds
    // a chart, so the grid row stretches it well past its own content. Rather
    // than leave 80px of nothing under the last row, the state list takes the
    // slack and the three rows share it evenly.
    <section
      className="mf-section mf-pane-fill"
      aria-label="Fleet availability"
      data-open={open ? "true" : "false"}
    >
      <Disclosure
        id="availability"
        label="Fleet availability"
        open={open}
        onToggle={onToggle}
        summary={`${counts.connected} / ${counts.total} connected`}
      >
        <div className="mt-4 flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <span className="mf-metric text-[28px] font-medium leading-none text-text-primary">
            {counts.connected}
            <span className="text-text-disabled"> / </span>
            <span className="text-text-tertiary">{counts.total}</span>
          </span>
          <span className="mf-kicker">connected</span>
        </div>

        {/* A list, not a <dl>: the two rows that count something wrong are
            buttons, and a <button> is not a valid child of a definition
            list. The dot / name / count reading order is unchanged. */}
        <ul className="mf-state-list mt-4">
          <StateRow tone="live" label="Online" count={counts.live} />
          <StateRow
            tone={reviewTone}
            label="Needs review"
            count={counts.needsReview}
            detail={reviewBreakdown(counts)}
            onActivate={onOpenAlerts}
          />
          <StateRow
            tone="offline"
            label="Offline"
            count={counts.offline}
            onActivate={onOpenAlerts}
          />
        </ul>
      </Disclosure>
    </section>
  );
}

function StateRow({
  tone,
  label,
  count,
  detail,
  onActivate,
}: {
  tone: "live" | "warning" | "critical" | "stale" | "offline";
  label: string;
  count: number;
  /** What is inside the bucket, for the tooltip and the accessible name. */
  detail?: string;
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
          aria-label={`${label}: ${machines}${inside}. Open alerts.`}
          title={detail && !empty ? `${detail} — open alerts` : "Open alerts"}
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
