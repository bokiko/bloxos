"use client";

import { useEffect, useState, useCallback } from "react";
import {
  AreaChart, Area, XAxis, YAxis, ResponsiveContainer, Tooltip,
} from "recharts";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { MetricsChartsSkeleton } from "./MetricsChartsSkeleton";

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

const tooltipStyle = {
  background: "#12121a",
  border: "1px solid #1e1e2e",
  borderRadius: 10,
  fontSize: 11,
  boxShadow: "0 8px 32px rgba(0,0,0,0.4)",
};

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
            <TabsTrigger key={p} value={p} className="text-sm px-4 py-1.5 font-mono tabular-nums">
              {p}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      {data.length < 2 ? (
        <MetricsChartsSkeleton hasGpu={hasGpu} />
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {/* CPU Chart */}
          <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
            <h4 className="text-xs font-medium text-blox-text mb-3">CPU Usage (%)</h4>
            <ResponsiveContainer width="100%" height={180}>
              <AreaChart data={data}>
                <defs>
                  <linearGradient id="cpuGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.25} />
                    <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <XAxis
                  dataKey="timestamp"
                  tickFormatter={formatTime}
                  tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  domain={[0, 100]}
                  tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                  axisLine={false}
                  tickLine={false}
                  width={30}
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={formatTooltipLabel}
                  formatter={(v) => [`${Number(v ?? 0).toFixed(1)}%`, "CPU"]}
                />
                <Area type="monotone" dataKey="cpu_percent" stroke="#3b82f6" fill="url(#cpuGrad)" strokeWidth={1.5} />
              </AreaChart>
            </ResponsiveContainer>
          </div>

          {/* RAM Chart */}
          <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
            <h4 className="text-xs font-medium text-blox-text mb-3">RAM Usage (GB)</h4>
            <ResponsiveContainer width="100%" height={180}>
              <AreaChart data={ramData}>
                <defs>
                  <linearGradient id="ramGrad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#8b5cf6" stopOpacity={0.25} />
                    <stop offset="95%" stopColor="#8b5cf6" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <XAxis
                  dataKey="timestamp"
                  tickFormatter={formatTime}
                  tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                  axisLine={false}
                  tickLine={false}
                  width={30}
                  tickFormatter={(v: number) => formatGB((v ?? 0) * 1024 ** 3)}
                />
                <Tooltip
                  contentStyle={tooltipStyle}
                  labelFormatter={formatTooltipLabel}
                  formatter={(v) => [`${Number(v ?? 0).toFixed(1)} GB`, "RAM"]}
                />
                <Area type="monotone" dataKey="ram_gb" stroke="#8b5cf6" fill="url(#ramGrad)" strokeWidth={1.5} />
              </AreaChart>
            </ResponsiveContainer>
          </div>

          {/* GPU Utilization Chart */}
          {hasGpu && (
            <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
              <h4 className="text-xs font-medium text-blox-text mb-3">GPU Utilization (%)</h4>
              <ResponsiveContainer width="100%" height={180}>
                <AreaChart data={gpuData}>
                  <defs>
                    <linearGradient id="gpuUtilGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#10b981" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="timestamp"
                    tickFormatter={formatTime}
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                  />
                  <YAxis
                    domain={[0, 100]}
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                    width={30}
                  />
                  <Tooltip
                    contentStyle={tooltipStyle}
                    labelFormatter={formatTooltipLabel}
                    formatter={(v) => [`${Number(v ?? 0).toFixed(1)}%`, "GPU Util"]}
                  />
                  <Area type="monotone" dataKey="gpu_util" stroke="#10b981" fill="url(#gpuUtilGrad)" strokeWidth={1.5} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}

          {/* GPU Temp Chart */}
          {hasGpu && (
            <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
              <h4 className="text-xs font-medium text-blox-text mb-3">GPU Temperature (C)</h4>
              <ResponsiveContainer width="100%" height={180}>
                <AreaChart data={gpuData}>
                  <defs>
                    <linearGradient id="gpuTempGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#f59e0b" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="#f59e0b" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="timestamp"
                    tickFormatter={formatTime}
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                  />
                  <YAxis
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                    width={30}
                  />
                  <Tooltip
                    contentStyle={tooltipStyle}
                    labelFormatter={formatTooltipLabel}
                    formatter={(v) => [`${Number(v ?? 0).toFixed(0)} C`, "GPU Temp"]}
                  />
                  <Area type="monotone" dataKey="gpu_temp" stroke="#f59e0b" fill="url(#gpuTempGrad)" strokeWidth={1.5} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}

          {/* VRAM Chart */}
          {hasGpu && (
            <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
              <h4 className="text-xs font-medium text-blox-text mb-3">VRAM Usage (GB)</h4>
              <ResponsiveContainer width="100%" height={180}>
                <AreaChart data={gpuData}>
                  <defs>
                    <linearGradient id="vramGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#10b981" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <XAxis
                    dataKey="timestamp"
                    tickFormatter={formatTime}
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                  />
                  <YAxis
                    tick={{ fontSize: 10, fill: "#6b6b80", fontFamily: "var(--font-mono)" }}
                    axisLine={false}
                    tickLine={false}
                    width={30}
                  />
                  <Tooltip
                    contentStyle={tooltipStyle}
                    labelFormatter={formatTooltipLabel}
                    formatter={(v) => [`${Number(v ?? 0).toFixed(1)} GB`, "VRAM"]}
                  />
                  <Area type="monotone" dataKey="vram_gb" stroke="#10b981" fill="url(#vramGrad)" strokeWidth={1.5} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
