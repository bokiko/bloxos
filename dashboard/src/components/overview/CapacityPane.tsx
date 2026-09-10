"use client";

// Monoform — "Capacity over time".
//
// There is no fleet-wide history in this product. FleetAggregate
// (components/fleet/fleetModel.ts) is entirely point-in-time, and the only
// history endpoints the hub exposes are per machine:
//   GET /api/machines/:id/metrics/history
//   GET /api/machines/:id/power/history
// So this pane states that plainly instead of drawing a line it would have to
// invent, and instead of fanning out one history request per machine to
// synthesise a series the backend does not have.

import { LineChart } from "lucide-react";
import { Disclosure } from "./Disclosure";

export function CapacityPane({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  return (
    <section
      className="mf-section min-w-0"
      aria-label="Capacity over time"
      data-open={open ? "true" : "false"}
    >
      <Disclosure
        id="capacity"
        label="Capacity over time"
        open={open}
        onToggle={onToggle}
        summary="not recorded fleet-wide"
      >
        {/* The box sizes to its content and is CENTRED in whatever height the
            paired-panel row gives the pane. It is deliberately not stretched:
            a dashed "no data" frame pulled to an arbitrary height reads as a
            chart that failed to load, which is a different and wrong claim.
            Centring uses the space without making that claim. */}
        <div className="flex h-full flex-col justify-center">
          <div className="mt-5 flex items-center gap-4 rounded-[10px] border border-dashed border-border-subtle px-5 py-6">
            <LineChart className="h-4 w-4 shrink-0 text-text-tertiary" aria-hidden="true" />
            <div className="min-w-0">
              <p className="text-[13px] font-medium text-text-primary">
                Fleet-wide history is not recorded yet.
              </p>
              <p className="mt-1.5 max-w-[62ch] text-[12px] leading-[1.55] text-text-tertiary">
                BloxOS stores metric and power history per machine, not for the fleet as a whole, so
                there is no aggregated series to chart. Open a machine to see its capacity over time.
              </p>
            </div>
          </div>
        </div>
      </Disclosure>
    </section>
  );
}
