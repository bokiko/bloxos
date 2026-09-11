"use client";

// Monoform — the single alert most worth naming, as one compact module.
//
// Needs attention beside it says how many there are. This one says which one,
// because a count is not actionable until it has a hostname attached: "3
// alerts" sends you to a list, "blx-07 · No heartbeat · 6m" sends you to a
// machine.
//
// It renders ONLY when there is a real active alert. With none it returns
// null, the workspace leaves it out of the module count, and the row closes
// up — an "all clear" card would be a second way of saying what Needs
// attention already says at zero, and an empty reserved cell would be a hole.

import Link from "next/link";
import { ArrowRight } from "lucide-react";
import type { AlertData } from "@/lib/demo-data";
import { urgentAlert } from "@/lib/overview-layout.mjs";
import { timeAgo } from "@/components/AlertPanel";

export function UrgentAlertMini({ alerts }: { alerts: AlertData[] }) {
  const alert = urgentAlert(alerts) as AlertData | null;
  if (!alert) return null;

  const critical = alert.severity === "critical";
  const name = alert.hostname || alert.machine_id;

  return (
    <section className="mf-overview-module" aria-label="Most urgent alert">
      <div className="mf-overview-module-head">
        <h3 className="mf-kicker">Most urgent alert</h3>
        <Link
          href={`/machine/${encodeURIComponent(alert.machine_id)}`}
          className="mf-inline-action"
          aria-label={`Open ${name}`}
        >
          Open
          <ArrowRight aria-hidden="true" />
        </Link>
      </div>

      <p className="mt-2 flex items-center gap-2">
        {/* Dot AND word: severity never rides on colour alone. */}
        <span
          className={`mf-status-dot ${critical ? "mf-status-critical" : "mf-status-warning"}`}
          aria-hidden="true"
        />
        <span className="truncate text-[13px] font-medium text-text-primary" title={name}>
          {name}
        </span>
        <span className={`mf-kicker ${critical ? "text-status-critical" : "text-status-warning"}`}>
          {alert.severity}
        </span>
      </p>

      <p className="mt-1 truncate text-[13px] text-text-secondary" title={alert.message}>
        {alert.message}
      </p>

      {/* The same age string the alert sheet prints, so the module and its
          destination cannot disagree about how old this is. */}
      {alert.triggered_at && (
        <p className="mf-kicker mt-1">{timeAgo(alert.triggered_at)}</p>
      )}
    </section>
  );
}
