"use client";

import { useEffect, useMemo, useState } from "react";
import { GaugeCluster } from "@/components/Gauge";
import { RankedBar } from "@/components/RankedBar";
import type { MachineMetrics } from "@/lib/demo-data";
import { computeFleetMetrics, isFreshMetrics } from "@/lib/fleet-metrics.mjs";

interface FleetOverviewProps {
  machines: MachineMetrics[];
}

export function FleetOverview({ machines }: FleetOverviewProps) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const activeMachines = useMemo(
    () => machines.filter((machine) => isFreshMetrics(machine.last_seen, now)),
    [machines, now]
  );
  const metrics = useMemo(() => computeFleetMetrics(activeMachines), [activeMachines]);

  const topGpuUtil = useMemo(() => {
    const items: Array<{ id: string; label: string; value: number }> = [];

    for (const m of activeMachines) {
      if (m.gpus && m.gpus.length > 0) {
        // For multi-GPU machines, use max utilization across all GPUs
        const maxUtil = Math.max(...m.gpus.map((g) => g.util_percent ?? 0));
        if (maxUtil > 0) {
          items.push({
            id: m.machine_id,
            label: m.hostname || m.machine_id,
            value: maxUtil,
          });
        }
      } else if ((m.gpu_util_percent ?? 0) > 0) {
        items.push({
          id: m.machine_id,
          label: m.hostname || m.machine_id,
          value: m.gpu_util_percent ?? 0,
        });
      }
    }

    return items;
  }, [activeMachines]);

  const topVram = useMemo(() => {
    const items: Array<{ id: string; label: string; value: number }> = [];

    for (const m of activeMachines) {
      if (m.gpus && m.gpus.length > 0) {
        // For multi-GPU machines, use max VRAM utilization
        const maxVram = Math.max(
          ...m.gpus.map((g) =>
            (g.mem_total_bytes ?? 0) > 0
              ? ((g.mem_used_bytes ?? 0) / g.mem_total_bytes) * 100
              : 0
          )
        );
        if (maxVram > 0) {
          items.push({
            id: m.machine_id,
            label: m.hostname || m.machine_id,
            value: maxVram,
          });
        }
      } else if ((m.gpu_vram_total_bytes ?? 0) > 0) {
        const vramPct = ((m.gpu_vram_used_bytes ?? 0) / (m.gpu_vram_total_bytes ?? 1)) * 100;
        items.push({
          id: m.machine_id,
          label: m.hostname || m.machine_id,
          value: vramPct,
        });
      }
    }

    return items;
  }, [activeMachines]);

  const gauges = useMemo(() => {
    const base = [
      {
        value: metrics.avgCpu,
        label: "Avg CPU",
        unit: "%",
        thresholds: [75, 92] as [number, number],
      },
      {
        value: metrics.avgRam,
        label: "Avg RAM",
        unit: "%",
        thresholds: [82, 95] as [number, number],
      },
    ];

    if (metrics.hasGpu) {
      base.push(
        {
          value: metrics.avgGpuUtil,
          label: "Avg GPU Util",
          unit: "%",
          thresholds: [70, 90] as [number, number],
        },
        {
          value: metrics.avgVramPct,
          label: "Avg VRAM",
          unit: "%",
          thresholds: [80, 95] as [number, number],
        },
        {
          value: metrics.maxGpuTemp,
          label: "Max GPU Temp",
          unit: "°C",
          thresholds: [78, 86] as [number, number],
        }
      );
    }

    if (metrics.diskPressure > 0) {
      base.push({
        value: metrics.diskPressure,
        label: "Avg Disk",
        unit: "%",
        thresholds: [85, 95] as [number, number],
      });
    }

    return base;
  }, [metrics]);

  if (machines.length === 0) {
    return null;
  }

  return (
    <div className="space-y-6">
      {/* Gauge cluster */}
      <div className="bg-surface-raised border border-border-subtle rounded-xl p-6">
        <div className="flex items-center justify-between mb-6">
          <h3 className="text-sm font-semibold text-text-primary uppercase tracking-wider">
            Fleet Health
          </h3>
          {metrics.hasGpu && metrics.totalPower > 0 && (
            <div className="flex items-baseline gap-2 px-3 py-1.5 rounded-lg bg-surface-sunken border border-border-subtle">
              <span className="text-xs text-text-tertiary uppercase tracking-wider font-medium">
                Fleet Power
              </span>
              <span className="text-lg font-mono tabular-nums text-text-primary font-semibold">
                {metrics.totalPower.toFixed(0)}
              </span>
              <span className="text-xs text-text-tertiary">W</span>
            </div>
          )}
        </div>
        {activeMachines.length > 0 ? (
          <GaugeCluster gauges={gauges} size="md" />
        ) : (
          <p className="text-sm text-text-secondary">No fresh metrics available.</p>
        )}
      </div>

      {/* Resource attribution */}
      {metrics.hasGpu && (topGpuUtil.length > 0 || topVram.length > 0) && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {topGpuUtil.length > 0 && (
            <RankedBar
              items={topGpuUtil}
              maxItems={5}
              title="Top GPU Utilization"
            />
          )}
          {topVram.length > 0 && (
            <RankedBar
              items={topVram}
              maxItems={5}
              title="Top VRAM Usage"
            />
          )}
        </div>
      )}
    </div>
  );
}
