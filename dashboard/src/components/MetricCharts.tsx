"use client";

import { useEffect, useState, useCallback } from "react";
import {
  CartesianGrid, Line, LineChart, XAxis, YAxis, ResponsiveContainer, Tooltip,
} from "recharts";
import { ChartTooltip } from "@/components/charts/ChartTooltip";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { MetricsChartsSkeleton } from "./MetricsChartsSkeleton";
import { MF_PANEL_HEAD, MF_PANEL_TITLE, MF_TAB } from "@/lib/monoform-classes";

type Period = "30m" | "1h" | "6h" | "24h" | "7d";

interface MetricPoint {
  timestamp: string;
  cpu_percent: number;
  ram_used: number;
  ram_total: number;
  gpu_temp: number;
  gpu_util: number;
  gpu_vram_used: number;
  gpu_vram_total: number;
}

interface MetricChartsProps {
  machineId: string;
  hasGpu: boolean;
}

function formatTime(ts: string | number | Date): string {
  const d = new Date(ts);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function formatTooltipLabel(label: unknown): string {
  if (typeof label === "string" || typeof label === "number" || label instanceof Date) {
    return formatTime(label);
  }
  return "";
}

function formatGB(bytes: number | undefined | null): string {
  return ((bytes ?? 0) / (1024 ** 3)).toFixed(1);
}

const periods: Period[] = ["30m", "1h", "6h", "24h", "7d"];

// Monoform: series colour identifies the *device*, never the health of the
// reading — CPU and RAM are the product blue, everything GPU is the GPU
// violet. Green / amber / red stay reserved for real nominal / warning /
// critical state, which a continuous line cannot honestly express. Flat
// strokes, no gradient fill.
const HOST_SERIES = "var(--mf-blue)";
const GPU_SERIES = "var(--mf-violet)";

const axisTick = {
  fontSize: 10,
  fill: "var(--text-tertiary)",
  fontFamily: "var(--font-mono)",
} as const;

const lineProps = {
  type: "monotone",
  dot: false,
  strokeWidth: 1.5,
  isAnimationActive: false,
} as const;

export function MetricCharts({ machineId, hasGpu }: MetricChartsProps) {
  const [period, setPeriod] = useState<Period>("1h");
  const [data, setData] = useState<MetricPoint[]>([]);

  const fetchData = useCallback(async () => {
    const token = getStoredToken();
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = `Bearer ${token}`;

    try {
      const res = await fetch(
        `${HUB_URL}/api/machines/${machineId}/metrics/history?period=${period}`,
        { headers }
      );
      if (!res.ok) return;
      const json = await res.json();
      setData(json.points || []);
    } catch { /* ignore */ }
  }, [machineId, period]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void fetchData();
    const interval = setInterval(() => {
      void fetchData();
    }, 30000);
    return () => clearInterval(interval);
  }, [fetchData]);

  const ramData = data.map((p) => ({
    ...p,
    ram_pct: (p.ram_total ?? 0) > 0 ? ((p.ram_used ?? 0) / p.ram_total) * 100 : 0,
    ram_gb: (p.ram_used ?? 0) / (1024 ** 3),
  }));

  const gpuData = data.map((p) => ({
    ...p,
    vram_pct: (p.gpu_vram_total ?? 0) > 0 ? ((p.gpu_vram_used ?? 0) / p.gpu_vram_total) * 100 : 0,
    vram_gb: (p.gpu_vram_used ?? 0) / (1024 ** 3),
  }));

  return (
    <div className="space-y-6">
      {/* Period selector */}
      <Tabs value={period} onValueChange={(v) => setPeriod(v as Period)}>
        <TabsList variant="line" className="gap-1">
          {periods.map((p) => (
            <TabsTrigger key={p} value={p} className={`${MF_TAB} font-mono tabular-nums`}>
              {p}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {data.length < 2 ? (
        <MetricsChartsSkeleton hasGpu={hasGpu} />
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Chart
            title="CPU"
            unit="%"
            data={data}
            dataKey="cpu_percent"
            stroke={HOST_SERIES}
            domain={[0, 100]}
            format={(v) => `${v.toFixed(1)}%`}
          />

          <Chart
            title="Memory"
            unit="GB"
            data={ramData}
            dataKey="ram_gb"
            stroke={HOST_SERIES}
            tickFormatter={(v) => formatGB(v * 1024 ** 3)}
            format={(v) => `${v.toFixed(1)} GB`}
          />

          {hasGpu && (
            <Chart
              title="GPU utilisation"
              unit="%"
              data={gpuData}
              dataKey="gpu_util"
              stroke={GPU_SERIES}
              domain={[0, 100]}
              format={(v) => `${v.toFixed(1)}%`}
            />
          )}

          {hasGpu && (
            <Chart
              title="GPU temperature"
              unit="°C"
              data={gpuData}
              dataKey="gpu_temp"
              stroke={GPU_SERIES}
              format={(v) => `${v.toFixed(0)}°C`}
            />
          )}

          {hasGpu && (
            <Chart
              title="VRAM"
              unit="GB"
              data={gpuData}
              dataKey="vram_gb"
              stroke={GPU_SERIES}
              tickFormatter={(v) => formatGB(v * 1024 ** 3)}
              format={(v) => `${v.toFixed(1)} GB`}
            />
          )}
        </div>
      )}
    </div>
  );
}

/**
 * One series over time. Every chart on this page is this component, so the
 * axes, grid, tooltip and stroke weight cannot drift apart between them.
 */
function Chart({
  title,
  unit,
  data,
  dataKey,
  stroke,
  domain,
  tickFormatter,
  format,
}: {
  title: string;
  unit: string;
  data: readonly object[];
  dataKey: string;
  stroke: string;
  domain?: [number, number];
  tickFormatter?: (value: number) => string;
  format: (value: number) => string;
}) {
  return (
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <h4 className={MF_PANEL_TITLE}>{title}</h4>
        <span className="mf-kicker">{unit}</span>
      </div>
      <div className="px-3 py-4">
        <ResponsiveContainer width="100%" height={180}>
          <LineChart data={data} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
            <CartesianGrid stroke="var(--border-subtle)" vertical={false} />
            <XAxis
              dataKey="timestamp"
              tickFormatter={formatTime}
              tick={axisTick}
              axisLine={false}
              tickLine={false}
              minTickGap={28}
            />
            <YAxis
              domain={domain}
              tick={axisTick}
              axisLine={false}
              tickLine={false}
              width={38}
              tickFormatter={tickFormatter}
            />
            <Tooltip
              cursor={{ stroke: "var(--border-strong)", strokeWidth: 1 }}
              content={
                <ChartTooltip
                  labelFormatter={formatTooltipLabel}
                  formatter={(v) => [v == null ? "—" : format(Number(v)), title]}
                />
              }
            />
            <Line {...lineProps} dataKey={dataKey} stroke={stroke} />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </section>
  );
}
