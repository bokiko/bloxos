"use client";

// Monoform — the Overview's date line.
//
// One kicker, and nothing else. The shell's top bar already renders "Overview"
// as the page's <h1>, so this block does not repeat it.
//
// It used to also carry a sentence about the fleet's posture ("Your fleet
// needs attention. Review the affected machines."). That sentence was read
// once and never again: the panes directly under it already show the same
// state as numbers, and a claim restating a number costs a line of prose on
// every visit. A dashboard is glanceable — figures and short labels — so the
// sentence is gone and the panes are what speak.

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

export function OverviewIntro() {
  const today = useSyncExternalStore<string | null>(
    subscribeToNothing,
    readToday,
    readNothing,
  );

  return (
    <header className="mf-overview-intro">
      {/* Non-breaking space reserves the line before the date resolves, so the
          panes below it do not jump on the first client paint. */}
      <span className="mf-kicker mf-overview-intro-date">{today ?? "\u00a0"}</span>
    </header>
  );
}
