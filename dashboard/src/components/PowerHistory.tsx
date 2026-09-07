"use client";

import { useEffect, useState } from "react";
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { mergePowerHistory, powerChartPoints, powerProblemLabel, powerSensorIDs, sampleAgeLabel } from "@/lib/power-history.mjs";

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
  const formatTime = (timestamp: number) => new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return (
    <section className="mt-6 space-y-3 border-t border-blox-border pt-5" aria-label="Component power history">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-blox-text">Power history · last 24 hours</h3>
          <p className="text-xs text-blox-muted">30-second averages and sampled peaks. Component power, not wall power.</p>
        </div>
        <select aria-label="Power sensor" value={sensor} onChange={(event) => setSensor(event.target.value)}
          className="max-w-full rounded border border-blox-border bg-blox-card p-2 text-xs text-blox-text">
          <option value="gpu_total">All GPUs together</option>
          {sensors.map((id) => <option key={id} value={id}>{id}</option>)}
          <option value="cpu">CPU packages{hasCPU ? "" : " (unavailable)"}</option>
        </select>
      </div>
      {error && <p role="status" className="text-xs text-amber-400">{error} Existing readings may be stale.</p>}
      {problem && <p role="status" className="text-xs text-amber-400">{problem}</p>}
      {(data?.degraded || (data?.gaps.length ?? 0) > 0 || points.some((point) => point.gap_before)) &&
        <p role="status" className="text-xs text-amber-400">History has gaps or reduced recording coverage. Missing data is not zero power.</p>}
      {points.length === 0 ? <p className="py-6 text-sm text-blox-muted">{problem ? "Power history is paused." : data
        ? "No power history yet. A supported agent normally sends its first completed window within about a minute."
        : error ? "Power history unavailable." : "Loading power history…"}</p> : <>
        <p className={`text-xs ${stale ? "text-amber-400" : "text-blox-muted"}`}>
          Last window: {now ? sampleAgeLabel(latest?.timestamp, now) : "—"}{stale ? " · stale" : ""}
          {latest?.coverage != null ? ` · ${Math.round(latest.coverage)}% sample coverage` : " · sensor unavailable"}
        </p>
        <div className="h-52" role="img" aria-label="Average and sampled peak power in watts over the last 24 hours">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chart}>
              <XAxis dataKey="timestamp" type="number" domain={["dataMin", "dataMax"]} tickFormatter={formatTime} tick={{ fontSize: 10 }} />
              <YAxis unit=" W" tick={{ fontSize: 10 }} width={64} />
              <Tooltip labelFormatter={(label) => formatTime(Number(label))}
                contentStyle={{ background: "#12121a", border: "1px solid #1e1e2e", fontSize: 12 }} />
              <Line dataKey="mean" name="Average (W)" stroke="#22c55e" dot={false} connectNulls={false} isAnimationActive={false} />
              <Line dataKey="peak" name="Sampled peak (W)" stroke="#f59e0b" strokeDasharray="4 3" dot={false} connectNulls={false} isAnimationActive={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
        <p className="text-xs text-blox-muted">Green: average · Amber: highest observed sample. Gaps and unavailable sensors are left blank.</p>
      </>}
    </section>
  );
}
