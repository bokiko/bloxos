"use client";

// Monoform — "Highest load".
//
// A real ranking from real point-in-time data. It used to print a bare
// percentage with no statement of what was being measured or sorted by, so
// `orangepi5-01 100% ▬▬▬ — GPU` left the reader guessing which of the two
// numbers ordered the list. The metric is now named in the pane and
// switchable: CPU (the original behaviour and the default), GPU utilisation,
// or memory used.
//
// NOTHING IS FABRICATED. A metric a machine does not report is `null`, and a
// null is excluded from the ranking rather than sorted as a zero — a CPU-only
// box is not "the least busy GPU". A metric no connected machine reports at
// all is offered as a disabled option, not as an empty list the reader has to
// interpret. There is no composite "load score": every number here is one
// sensor reading, which is why it can be ranked honestly at all.
//
// Offline machines are excluded throughout: their last reading is not current
// load.

import { useMemo } from "react";
import Link from "next/link";
import type { MachineMetrics } from "@/lib/demo-data";
import { machineGpuUtil, ramPct, statusOf, type Metric } from "@/components/fleet/fleetModel";
import { Disclosure } from "./Disclosure";
import type { LoadMetric } from "./useWorkspacePrefs";

const LIMIT = 5;

interface MetricSpec {
  /** Switch label. */
  label: string;
  /** What the percentage means, in a sentence. */
  noun: string;
  /** The reading, or null when this machine does not report it. */
  read: (m: MachineMetrics) => Metric;
  /** Meter colour: blue for CPU and memory, violet for GPU (table convention). */
  gpuTone?: boolean;
  /** The second reading shown beside the ranked one. */
  companion: LoadMetric;
}

const METRICS: Record<LoadMetric, MetricSpec> = {
  cpu: {
    label: "CPU",
    noun: "CPU utilisation",
    read: (m) => (Number.isFinite(m.cpu_percent) && m.cpu_percent >= 0 ? m.cpu_percent : null),
    companion: "gpu",
  },
  gpu: {
    label: "GPU",
    noun: "GPU utilisation",
    read: machineGpuUtil,
    gpuTone: true,
    companion: "cpu",
  },
  memory: {
    label: "Memory",
    noun: "memory used",
    read: ramPct,
    companion: "cpu",
  },
};

const ORDER: LoadMetric[] = ["cpu", "gpu", "memory"];

interface LoadRow {
  machineId: string;
  name: string;
  value: number;
  companion: Metric;
}

export interface HighestLoadPaneProps {
  machines: MachineMetrics[];
  now: number;
  metric: LoadMetric;
  onMetricChange: (metric: LoadMetric) => void;
  open: boolean;
  onToggle: () => void;
}

export function HighestLoadPane({
  machines,
  now,
  metric,
  onMetricChange,
  open,
  onToggle,
}: HighestLoadPaneProps) {
  const spec = METRICS[metric];
  const companionSpec = METRICS[spec.companion];

  const connected = useMemo(
    () => machines.filter((m) => statusOf(m) !== "offline"),
    // statusOf reads the wall clock, so the tick is a deliberate dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [machines, now],
  );

  // Which metrics this fleet can actually be ranked by right now. An option
  // with no readings behind it is disabled rather than silently empty.
  const available = useMemo(() => {
    const out = {} as Record<LoadMetric, boolean>;
    for (const key of ORDER) {
      out[key] = connected.some((m) => METRICS[key].read(m) !== null);
    }
    return out;
  }, [connected]);

  const rows = useMemo<LoadRow[]>(
    () =>
      connected
        .map((m) => ({
          machineId: m.machine_id,
          name: m.hostname || m.machine_id,
          value: spec.read(m),
          companion: companionSpec.read(m),
        }))
        .filter((r): r is LoadRow => r.value !== null)
        .sort((a, b) => b.value - a.value)
        .slice(0, LIMIT),
    [connected, spec, companionSpec],
  );

  const summary =
    rows.length > 0
      ? `${rows[0].name} ${Math.round(rows[0].value)}% ${spec.label}`
      : `no ${spec.label} data`;

  const metricSwitch = (
    <div className="mf-segment" role="group" aria-label="Rank machines by">
      {ORDER.map((key) => {
        const selected = key === metric;
        return (
          <button
            key={key}
            type="button"
            aria-pressed={selected}
            // The selected option stays operable even when the fleet stops
            // reporting it, so the switch never contradicts the list below it.
            disabled={!available[key] && !selected}
            onClick={() => onMetricChange(key)}
            title={
              available[key]
                ? `Rank by ${METRICS[key].noun}`
                : `No connected machine reports ${METRICS[key].noun}`
            }
          >
            {METRICS[key].label}
          </button>
        );
      })}
    </div>
  );

  return (
    <section
      className="mf-section min-w-0"
      aria-label="Highest load"
      data-open={open ? "true" : "false"}
    >
      <Disclosure
        id="load"
        label="Highest load"
        open={open}
        onToggle={onToggle}
        summary={summary}
        actions={open ? metricSwitch : null}
      >
        <p className="mf-table-meta mt-3">Ranked by {spec.noun}, highest first</p>

        {rows.length === 0 ? (
          <p className="mt-5 text-[13px] text-text-secondary">
            No connected machine is reporting {spec.noun}.
          </p>
        ) : (
          <ol className="mt-4 border-t border-border-subtle">
            {rows.map((row, i) => (
              <li
                key={row.machineId}
                className="flex items-center gap-3 border-b border-border-subtle py-3 last:border-b-0"
              >
                <span className="mf-metric w-4 shrink-0 text-[11px] text-text-disabled">
                  {i + 1}
                </span>
                <Link
                  href={`/machine/${row.machineId}`}
                  className="min-w-0 flex-1 truncate text-[13px] text-text-primary transition-colors duration-[var(--motion-fast)] hover:text-accent"
                >
                  {row.name}
                </Link>
                <span className="flex shrink-0 items-center">
                  <span className="mf-metric w-10 text-right text-[13px] text-text-primary">
                    {Math.round(row.value)}%
                  </span>
                  <span
                    className={spec.gpuTone ? "mf-meter mf-meter--gpu" : "mf-meter"}
                    role="img"
                    aria-label={`${spec.noun} ${Math.round(row.value)} percent`}
                  >
                    <i style={{ width: `${Math.min(100, Math.max(0, row.value))}%` }} />
                  </span>
                </span>
                {/* The companion reading. A machine with no GPU prints a dash,
                    never a fabricated 0%. */}
                <span className="mf-metric hidden w-[74px] shrink-0 text-right text-[11px] text-text-tertiary lg:inline">
                  {row.companion === null
                    ? `— ${companionSpec.label}`
                    : `${Math.round(row.companion)}% ${companionSpec.label}`}
                </span>
              </li>
            ))}
          </ol>
        )}
      </Disclosure>
    </section>
  );
}
