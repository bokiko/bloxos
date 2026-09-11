"use client";

// Monoform — the Overview's intro row: one line of fleet state, and the
// control that changes what is shown below it.
//
// The date moved to the top bar's kicker, above the "Overview" title the shell
// already renders, so it is read where every other page's context is read.
//
// What is left is ONE line, and it is numbers: "All 6 machines live", or
// "4 of 6 live · 1 needs review · 1 offline". It is computed from the same
// counts the modules below use, never from an adjective — the earlier version
// of this line said "Your fleet needs attention. Review the affected
// machines.", which was a mood and an instruction restating a number nobody
// had read yet. No second clause, no claim, nothing that is not measured.
//
// This is also the only <header> the Overview may have: the shell owns the
// page header, and a test enforces that app/page.tsx contains none.

import type { ReactNode } from "react";
import { useSyncExternalStore } from "react";

/** "Wednesday, 10 September" — weekday and month names follow the browser locale. */
function formatToday(date: Date): string {
  const weekday = date.toLocaleDateString(undefined, { weekday: "long" });
  const day = date.toLocaleDateString(undefined, { day: "numeric" });
  const month = date.toLocaleDateString(undefined, { month: "long" });
  return `${weekday}, ${day} ${month}`;
}

// The date is a client-only value: the server has no idea what timezone or
// locale the reader is in, so producing it during SSR would guarantee a
// hydration mismatch. useSyncExternalStore is the primitive for exactly this —
// a null server snapshot, the real date on the client. Nothing changes it
// while the page is open, so the store never notifies.
const subscribeToNothing = () => () => {};
const readToday = () => formatToday(new Date());
const readNothing = () => null;

/**
 * The formatted date, for whoever wants to render it. The Overview hands it to
 * `usePageTitle` so it lands in the top bar's kicker; it is exported from here
 * because this is where the hydration-safe read already lives.
 */
export function useToday(): string | null {
  return useSyncExternalStore<string | null>(subscribeToNothing, readToday, readNothing);
}

export function OverviewIntro({
  health,
  tone,
  actions,
}: {
  /** One line of measured fleet state. */
  health: string;
  tone: "ok" | "warning" | "critical" | "neutral";
  /** The Customize control. */
  actions?: ReactNode;
}) {
  const dotTone =
    tone === "ok" ? "mf-status-live" : tone === "neutral" ? "mf-status-none" : `mf-status-${tone}`;

  return (
    <header className="mf-overview-intro">
      <p role="status" className="flex items-center gap-2 text-[13px] text-text-secondary">
        <span className={`mf-status-dot ${dotTone}`} aria-hidden="true" />
        {health}
      </p>
      {actions && <div className="mf-overview-intro-actions">{actions}</div>}
    </header>
  );
}
