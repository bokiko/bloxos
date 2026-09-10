"use client";

// Monoform — the Overview page's opening statement.
//
// The shell's top bar already renders "Overview" as the page title, so this
// block deliberately does NOT repeat the word. It opens with the real local
// date and goes straight to a sentence that states the fleet's actual
// posture. The three sentences are the only ones this component can say, and
// which one appears is decided from live machine state by the caller.
//
// It is ONE line. This used to be a 46px display headline stacked under its
// own date kicker, beside a fixed sentence ("Live infrastructure state across
// your production workspace") that restated the page's existence and told the
// operator nothing — about 150px of screen to carry one fact. That sentence is
// gone and the date has moved onto the same baseline as the claim, so the
// fleet-posture grid starts near the top of the viewport instead of below it.

import { useSyncExternalStore } from "react";

export type FleetPosture = "healthy" | "attention" | "waiting";

const HEADLINES: Record<FleetPosture, readonly [string, string]> = {
  healthy: ["Your fleet is healthy.", "Here is what changed."],
  attention: ["Your fleet needs attention.", "Review the affected machines."],
  waiting: [
    "Waiting for fleet telemetry.",
    "The workspace will update when machines report.",
  ],
};

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

export function OverviewIntro({ posture }: { posture: FleetPosture }) {
  const today = useSyncExternalStore<string | null>(
    subscribeToNothing,
    readToday,
    readNothing,
  );

  const [lead, rest] = HEADLINES[posture];

  return (
    <header className="mf-overview-intro">
      {/* Non-breaking space reserves the line before the date resolves, so the
          sentence beside it does not jump on the first client paint. */}
      <span className="mf-kicker mf-overview-intro-date">{today ?? "\u00a0"}</span>
      <h1>
        <strong>{lead}</strong> {rest}
      </h1>
    </header>
  );
}
