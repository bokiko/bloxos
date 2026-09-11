"use client";

import { useEffect, useState } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { ChartTooltip } from "@/components/charts/ChartTooltip";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { mergePowerHistory, powerChartPoints, powerProblemLabel, powerRailStats, powerSensorIDs, sampleAgeLabel } from "@/lib/power-history.mjs";
import { MF_INPUT, MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";

// Monoform: average and peak are two views of the same measurement, not two
// health states, so they are told apart by stroke style — solid measured-power
// teal for the average, dashed violet for the sampled peak — and never by the
// green/amber pair this product reserves for real nominal/warning data.
const MEAN_STROKE = "var(--data-power, var(--mf-blue))";
const PEAK_STROKE = "var(--mf-violet)";
const axisTick = { fontSize: 10, fill: "var(--text-tertiary)", fontFamily: "var(--font-mono)" } as const;

interface Stats { mean_watts: number | null; peak_watts: number | null; samples: number }
interface Point {
  stream_id: string; seq: number; start_unix_ms: number; end_unix_ms: number;
  expected_samples: number; gap_before?: boolean;
  gpus: (Stats & { id: string })[]; gpu_total?: Stats; cpu?: Stats;
}
interface History { points: Point[]; gaps: { stream_id: string; from: number; through: number }[]; degraded: boolean; cursor: number; problem?: string }

export function PowerHistory({ machineId }: { machineId: string }) {
  const [sensor, setSensor] = useState("gpu_total");
  const [state, setState] = useState<{ machineId: string; data?: History; error?: string }>({ machineId });
  const [now, setNow] = useState(0);
  useEffect(() => {
    let stopped = false;
    let active: AbortController | undefined;
    let cached: History | undefined;
    const load = async () => {
      if (active) return;
      const controller = new AbortController();
      active = controller;
      const timeout = setTimeout(() => controller.abort(), 10000);
      try {
        const token = getStoredToken();
        const after = cached ? `?after=${cached.cursor}` : "";
        const res = await fetch(`${HUB_URL}/api/machines/${encodeURIComponent(machineId)}/power/history${after}`, {
          headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: controller.signal,
        });
        if (cached && (res.status === 400 || res.status === 409)) {
          throw new RangeError("Power history changed on the hub; reloading.");
        }
        if (!res.ok) throw new Error(res.status === 404
          ? "Power history is not available on this hub yet." : "Could not refresh power history.");
        const data = await res.json() as History;
        cached = mergePowerHistory(cached, data, Date.now()) as History;
        if (!stopped) setState({ machineId, data: cached });
      } catch (error) {
        if (error instanceof RangeError) cached = undefined;
        if (!stopped) setState((previous) => ({ machineId,
          data: previous.machineId === machineId ? previous.data : undefined,
          error: error instanceof Error ? error.message : "Could not refresh power history." }));
      } finally { clearTimeout(timeout); active = undefined; }
    };
    void load();
    const refresh = setInterval(() => void load(), 30000);
    const clock = setInterval(() => setNow(Date.now()), 1000);
    return () => { stopped = true; active?.abort(); clearInterval(refresh); clearInterval(clock); };
  }, [machineId]);
  const data = state.machineId === machineId ? state.data : undefined;
  const points = data?.points ?? [];
  const sensors = powerSensorIDs(points) as string[];
  const chart = powerChartPoints(points, sensor);
  const latest = chart.at(-1);
  const error = state.machineId === machineId ? state.error : undefined;
  const problem = powerProblemLabel(data?.problem);
  const hasCPU = points.some((point) => point.cpu && point.cpu.samples > 0);
  const stale = latest && now > 0 && now - latest.timestamp > 90000;
  const rail = powerRailStats(points, sensor) as {
    windows: number;
    samples: number;
    average: number | null;
    peak: number | null;
  };
  const formatTime = (timestamp: number) => new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <section className="mf-panel mf-machine-power overflow-hidden" aria-label="Component power history">
      <div className={MF_PANEL_HEAD}>
        <div>
          <h2 className={MF_PANEL_TITLE}>Power history · last 24 hours</h2>
          <p className="mt-1 text-xs text-text-tertiary">
            30-second averages and sampled peaks. Component power, not wall power.
          </p>
        </div>
        <select
          aria-label="Power sensor"
          value={sensor}
          onChange={(event) => setSensor(event.target.value)}
          className={`${MF_INPUT} max-w-full border px-2.5`}
        >
          <option value="gpu_total">All GPUs together</option>
          {sensors.map((id) => <option key={id} value={id}>{id}</option>)}
          <option value="cpu">CPU packages{hasCPU ? "" : " (unavailable)"}</option>
        </select>
      </div>
      <div className="space-y-3.5 px-6 py-5">
      {error && (
        <p role="status" className="text-xs text-status-warning">
          {error} Existing readings may be stale.
        </p>
      )}
      {problem && <p role="status" className="text-xs text-status-warning">{problem}</p>}
      {(data?.degraded || (data?.gaps.length ?? 0) > 0 || points.some((point) => point.gap_before)) && (
        <p role="status" className="text-xs text-status-warning">
          History has gaps or reduced recording coverage. Missing data is not zero power.
        </p>
      )}
      {points.length === 0 ? (
        <p className="py-6 text-[13px] text-text-tertiary">{problem ? "Power history is paused." : data
          ? "No power history yet. A supported agent normally sends its first completed window within about a minute."
          : error ? "Power history unavailable." : "Loading power history…"}</p>
      ) : (
        <div className="mf-machine-power-layout">
          {/* The numbers this chart is made of. None of them is a new
              measurement: the average is the sample-weighted mean of the
              windows actually loaded (so a half-sampled window counts for
              half), the peak is the highest single sampled peak of any window
              and never a sum across sensors, and coverage is the LATEST
              window's — a 24-hour ratio would read as a fault on a machine
              enrolled an hour ago. A null reading prints an em dash, never 0. */}
          <dl className="mf-power-rail">
            <RailRow label="Latest 30 s average" value={formatWatts(latest?.mean)} lead />
            <RailRow
              label={`Average · ${rail.windows} window${rail.windows === 1 ? "" : "s"}`}
              value={formatWatts(rail.average)}
              title={`Sample-weighted mean of the ${rail.samples} samples in the ${rail.windows} completed windows loaded`}
            />
            <RailRow label="Highest sampled peak" value={formatWatts(rail.peak)} />
            <RailRow
              label="Last window"
              value={`${now ? sampleAgeLabel(latest?.timestamp, now) : "—"}${stale ? " · stale" : ""}`}
              tone={stale ? "warning" : undefined}
            />
            <RailRow
              label="Coverage"
              value={latest?.coverage != null ? `${Math.round(latest.coverage)}% sample coverage` : "sensor unavailable"}
            />
          </dl>
          <div>
          <div
            className="h-52"
            role="img"
            aria-label="Average and sampled peak power in watts over the last 24 hours"
          >
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={chart} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid stroke="var(--border-subtle)" vertical={false} />
                <XAxis
                  dataKey="timestamp" type="number" domain={["dataMin", "dataMax"]}
                  tickFormatter={formatTime} tick={axisTick} axisLine={false} tickLine={false} minTickGap={28}
                />
                <YAxis unit=" W" tick={axisTick} axisLine={false} tickLine={false} width={64} />
                <Tooltip
                  cursor={{ stroke: "var(--border-strong)", strokeWidth: 1 }}
                  content={
                    <ChartTooltip
                      labelFormatter={(label) => formatTime(Number(label))}
                      formatter={(value, name) => [
                        value == null ? "—" : `${Math.round(Number(value))} W`,
                        String(name ?? ""),
                      ]}
                    />
                  }
                />
                <Line dataKey="mean" name="Average (W)" stroke={MEAN_STROKE} strokeWidth={1.5}
                  dot={false} connectNulls={false} isAnimationActive={false} />
                <Line dataKey="peak" name="Sampled peak (W)" stroke={PEAK_STROKE} strokeWidth={1.5}
                  strokeDasharray="4 3" dot={false} connectNulls={false} isAnimationActive={false} />
              </LineChart>
            </ResponsiveContainer>
          </div>
          <p className="mt-2 text-xs text-text-tertiary">
            Solid line: average · dashed line: highest observed sample. Gaps and unavailable sensors
            are left blank.
          </p>
          </div>
        </div>
      )}
      </div>
    </section>
  );
}

/** One number on the rail. `lead` is the one the eye lands on first. */
function RailRow({
  label,
  value,
  lead,
  tone,
  title,
}: {
  label: string;
  value: string;
  lead?: boolean;
  tone?: "warning";
  title?: string;
}) {
  return (
    <div>
      <dt className="mf-kicker">{label}</dt>
      <dd
        className={`mf-metric ${lead ? "mf-power-rail-value" : "mt-1 text-[13px]"} ${
          tone === "warning" ? "text-status-warning" : lead ? "" : "text-text-primary"
        }`}
        title={title}
      >
        {value}
      </dd>
    </div>
  );
}

/** Watts, or an em dash. A missing reading is never printed as zero. */
function formatWatts(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? "—" : `${Math.round(value)} W`;
}
