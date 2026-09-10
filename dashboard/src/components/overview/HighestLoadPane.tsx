"use client";

// Monoform — "Highest load".
//
// A real ranking from real point-in-time data: the busiest connected machines
// by CPU utilisation, which every machine reports (GPU utilisation is shown
// alongside only when the machine actually has a GPU — a CPU-only box is a
// dash, never a fabricated 0%). Offline machines are excluded: their last
// reading is not current load.

import { useMemo } from "react";
import Link from "next/link";
import type { MachineMetrics } from "@/lib/demo-data";
import { machineGpuUtil, statusOf } from "@/components/fleet/fleetModel";

const LIMIT = 5;

interface LoadRow {
  machineId: string;
  name: string;
  cpu: number;
  gpu: number | null;
}

export function HighestLoadPane({ machines, now }: { machines: MachineMetrics[]; now: number }) {
  const rows = useMemo<LoadRow[]>(() => {
    return machines
      .filter((m) => statusOf(m) !== "offline")
      .filter((m) => Number.isFinite(m.cpu_percent) && m.cpu_percent >= 0)
      .map((m) => ({
        machineId: m.machine_id,
        name: m.hostname || m.machine_id,
        cpu: m.cpu_percent,
        gpu: machineGpuUtil(m),
      }))
      .sort((a, b) => b.cpu - a.cpu)
      .slice(0, LIMIT);
    // statusOf reads the wall clock, so the tick is a deliberate dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, now]);

  return (
    <section className="mf-panel min-w-0 px-7 py-6" aria-label="Highest load">
      <div className="mf-kicker">Highest load</div>

      {rows.length === 0 ? (
        <p className="mt-5 text-[13px] text-text-secondary">
          No connected machine is reporting CPU utilisation.
        </p>
      ) : (
        <ol className="mt-5 border-t border-border-subtle">
          {rows.map((row, i) => (
            <li
              key={row.machineId}
              className="flex items-center gap-3 border-b border-border-subtle py-3 last:border-b-0"
            >
              <span className="mf-metric w-4 shrink-0 text-[11px] text-text-disabled">{i + 1}</span>
              <Link
                href={`/machine/${row.machineId}`}
                className="min-w-0 flex-1 truncate text-[13px] text-text-primary transition-colors duration-[var(--motion-fast)] hover:text-accent"
              >
                {row.name}
              </Link>
              <span className="flex shrink-0 items-center">
                <span className="mf-metric w-10 text-right text-[13px] text-text-primary">
                  {Math.round(row.cpu)}%
                </span>
                <span
                  className="mf-meter"
                  role="img"
                  aria-label={`CPU ${Math.round(row.cpu)} percent`}
                >
                  <i style={{ width: `${Math.min(100, Math.max(0, row.cpu))}%` }} />
                </span>
              </span>
              <span className="mf-metric hidden w-16 shrink-0 text-right text-[11px] text-text-tertiary lg:inline">
                {row.gpu === null ? "— GPU" : `${Math.round(row.gpu)}% GPU`}
              </span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
