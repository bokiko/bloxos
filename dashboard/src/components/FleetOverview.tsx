"use client";

import { useMemo } from "react";
import { GaugeCluster } from "@/components/Gauge";
import { RankedBar } from "@/components/RankedBar";
import type { MachineMetrics } from "@/lib/demo-data";
import { classifyMachine } from "@/components/StatusBadge";

interface FleetOverviewProps {
  machines: MachineMetrics[];
}

interface FleetMetrics {
  avgCpu: number;
  avgRam: number;
  avgGpuUtil: number;
  avgVramPct: number;
  maxGpuTemp: number;
  totalPower: number;
  diskPressure: number;
  hasGpu: boolean;
  hasMultiGpu: boolean;
}

function computeFleetMetrics(machines: MachineMetrics[]): FleetMetrics {
  const activeMachines = machines.filter((m) => {
    const status = classifyMachine(m).status;
    return status !== "offline";
  });

  if (activeMachines.length === 0) {
    return {
      avgCpu: 0,
      avgRam: 0,
      avgGpuUtil: 0,
      avgVramPct: 0,
      maxGpuTemp: 0,
      totalPower: 0,
      diskPressure: 0,
      hasGpu: false,
      hasMultiGpu: false,
    };
  }

  let cpuSum = 0;
  let cpuCount = 0;
  let ramSum = 0;
  let ramCount = 0;
  let gpuUtilSum = 0;
  let gpuUtilCount = 0;
  let vramSum = 0;
  let vramCount = 0;
  let maxGpuTemp = 0;
  let totalPower = 0;
  let diskSum = 0;
  let diskCount = 0;
  let gpuCount = 0;

  for (const m of activeMachines) {
    if ((m.cpu_percent ?? 0) >= 0) {
      cpuSum += m.cpu_percent;
      cpuCount++;
    }

    if ((m.ram_total_bytes ?? 0) > 0) {
      const ramPct = ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100;
      ramSum += ramPct;
      ramCount++;
    }

    if ((m.disk_total_bytes ?? 0) > 0) {
      const diskPct = ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100;
      diskSum += diskPct;
      diskCount++;
    }

    // GPU metrics - prefer per-GPU data if available
    if (m.gpus && m.gpus.length > 0) {
      gpuCount += m.gpus.length;
      for (const gpu of m.gpus) {
        if ((gpu.util_percent ?? 0) >= 0) {
          gpuUtilSum += gpu.util_percent;
          gpuUtilCount++;
        }
        if ((gpu.mem_total_bytes ?? 0) > 0) {
          const vramPct = ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100;
          vramSum += vramPct;
          vramCount++;
        }
        if ((gpu.temp_c ?? 0) > maxGpuTemp) {
          maxGpuTemp = gpu.temp_c;
        }
        if ((gpu.power_watts ?? 0) > 0) {
          totalPower += gpu.power_watts;
        }
      }
    } else {
      // Fallback to machine-level GPU metrics
      if ((m.gpu_util_percent ?? 0) >= 0) {
        gpuUtilSum += m.gpu_util_percent ?? 0;
        gpuUtilCount++;
        gpuCount++;
      }
      if ((m.gpu_vram_total_bytes ?? 0) > 0) {
        const vramPct = ((m.gpu_vram_used_bytes ?? 0) / (m.gpu_vram_total_bytes ?? 1)) * 100;
        vramSum += vramPct;
        vramCount++;
      }
      if ((m.gpu_temp ?? 0) > maxGpuTemp) {
        maxGpuTemp = m.gpu_temp ?? 0;
      }
    }
  }

  return {
    avgCpu: cpuCount > 0 ? cpuSum / cpuCount : 0,
    avgRam: ramCount > 0 ? ramSum / ramCount : 0,
    avgGpuUtil: gpuUtilCount > 0 ? gpuUtilSum / gpuUtilCount : 0,
    avgVramPct: vramCount > 0 ? vramSum / vramCount : 0,
    maxGpuTemp,
    totalPower,
    diskPressure: diskCount > 0 ? diskSum / diskCount : 0,
    hasGpu: gpuCount > 0,
    hasMultiGpu: gpuCount > 1,
  };
}

export function FleetOverview({ machines }: FleetOverviewProps) {
  const metrics = useMemo(() => computeFleetMetrics(machines), [machines]);

  const topGpuUtil = useMemo(() => {
    const items: Array<{ id: string; label: string; value: number }> = [];
    
    for (const m of machines) {
      const status = classifyMachine(m).status;
      if (status === "offline") continue;

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
  }, [machines]);

  const topVram = useMemo(() => {
    const items: Array<{ id: string; label: string; value: number }> = [];

    for (const m of machines) {
      const status = classifyMachine(m).status;
      if (status === "offline") continue;

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
  }, [machines]);

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
        <GaugeCluster gauges={gauges} size="md" />
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
