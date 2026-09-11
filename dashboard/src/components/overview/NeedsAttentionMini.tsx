"use client";

// Monoform — active hub alerts, as one compact module.
//
// This is the ONLY module built from the hub's alert list (GET /api/alerts
// plus the SSE `alert` stream), which is why it is the one whose control opens
// the alert sheet. Fleet availability beside it counts machine STATE
// classified on this client; the two can legitimately disagree — a stale
// machine with no rule firing, an acknowledged alert whose condition has
// passed — and neither is wrong. Saying so once, on the tooltip, is cheaper
// than a paragraph and more honest than quietly making the numbers match.
//
// At zero it renders quiet rather than disappearing. A healthy fleet and a
// module that failed to load must not look the same, and "0 · No active
// alerts" is three words that say which one this is. There is no control at
// zero: a button opening an empty sheet lies about there being something
// behind it.

import { ArrowRight } from "lucide-react";
import type { AlertData } from "@/lib/demo-data";
import { alertBreakdown } from "@/lib/overview-layout.mjs";

export function NeedsAttentionMini({
  alerts,
  onOpenAlerts,
}: {
  alerts: AlertData[];
  onOpenAlerts: () => void;
}) {
  const { total, critical, warning } = alertBreakdown(alerts) as {
    total: number;
    critical: number;
    warning: number;
  };

  // The count wears the worst severity present. At zero it goes quiet — the
  // severity colours stay reserved for severity.
  const tone =
    total === 0 ? "text-text-disabled" : critical > 0 ? "text-status-critical" : "text-status-warning";

  return (
    <section className="mf-overview-module" aria-label="Needs attention">
      <div className="mf-overview-module-head">
        <h3 className="mf-kicker">Needs attention</h3>
        {total > 0 && (
          <button type="button" className="mf-inline-action" onClick={onOpenAlerts}>
            View alerts
            <ArrowRight aria-hidden="true" />
          </button>
        )}
      </div>

      <p
        className="mt-2"
        title="Active alerts the hub has sent. Alert rules and machine state are measured separately, so these numbers and Fleet availability can differ."
      >
        <span className={`mf-metric text-[24px] font-medium leading-none ${tone}`}>{total}</span>
      </p>

      {total === 0 ? (
        <p className="mt-2 text-[13px] text-text-tertiary">No active alerts</p>
      ) : (
        <p className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-text-secondary">
          {/* Only the severities the hub actually emits, and only the ones
              present — a "0 critical" chip is noise on a fleet with none. */}
          {critical > 0 && (
            <span className="inline-flex items-center gap-1.5">
              <span className="mf-status-dot mf-status-critical" aria-hidden="true" />
              {critical} critical
            </span>
          )}
          {warning > 0 && (
            <span className="inline-flex items-center gap-1.5">
              <span className="mf-status-dot mf-status-warning" aria-hidden="true" />
              {warning} warning
            </span>
          )}
        </p>
      )}
    </section>
  );
}
