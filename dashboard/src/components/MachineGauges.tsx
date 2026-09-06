"use client";

import { Gauge } from "@/components/Gauge";

interface GPUData {
  index: number;
  name: string;
  temp_c: number;
  util_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  power_watts: number;
  fan_percent: number;
}

interface MachineMetrics {
  cpu_percent: number;
  cpu_temp_c?: number;
  ram_used_bytes: number;
  ram_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  gpu_temp: number;
  gpu_util_percent: number;
  gpu_vram_used_bytes: number;
  gpu_vram_total_bytes: number;
}

interface MachineGaugesProps {
  metrics: MachineMetrics;
  gpus?: GPUData[];
}

export function MachineGauges({ metrics, gpus }: MachineGaugesProps) {
  const ramPct =
    (metrics.ram_total_bytes ?? 0) > 0
      ? ((metrics.ram_used_bytes ?? 0) / metrics.ram_total_bytes) * 100
      : 0;

  const diskPct =
    (metrics.disk_total_bytes ?? 0) > 0
      ? ((metrics.disk_used_bytes ?? 0) / metrics.disk_total_bytes) * 100
      : 0;

  const hasMultiGpu = gpus && gpus.length > 1;
  const hasGpu =
    (gpus && gpus.length > 0) ||
    (!gpus && (metrics.gpu_temp > 0 || metrics.gpu_util_percent > 0));

  // Main gauges (always shown)
  const mainGauges = [
    {
      value: metrics.cpu_percent ?? 0,
      label: "CPU",
      unit: "%",
      thresholds: [75, 92] as [number, number],
    },
    {
      value: ramPct,
      label: "RAM",
      unit: "%",
      thresholds: [82, 95] as [number, number],
    },
  ];

  // GPU metrics - show main radials for any GPU configuration
  if (hasGpu) {
    let gpuUtil = 0;
    let gpuTemp = 0;
    let vramPct = 0;

    if (gpus && gpus.length > 0) {
      // For multi-GPU: use max utilization, max temp, max VRAM% across all GPUs
      gpuUtil = Math.max(...gpus.map((g) => g.util_percent ?? 0));
      gpuTemp = Math.max(...gpus.map((g) => g.temp_c ?? 0));
      const vramPcts = gpus
        .filter((g) => (g.mem_total_bytes ?? 0) > 0)
        .map((g) => ((g.mem_used_bytes ?? 0) / g.mem_total_bytes) * 100);
      vramPct = vramPcts.length > 0 ? Math.max(...vramPcts) : 0;
    } else {
      // Fallback to machine-level metrics
      gpuUtil = metrics.gpu_util_percent ?? 0;
      gpuTemp = metrics.gpu_temp ?? 0;
      vramPct =
        (metrics.gpu_vram_total_bytes ?? 0) > 0
          ? ((metrics.gpu_vram_used_bytes ?? 0) / metrics.gpu_vram_total_bytes) * 100
          : 0;
    }

    mainGauges.push(
      {
        value: gpuUtil,
        label: hasMultiGpu ? "Max GPU Util" : "GPU Util",
        unit: "%",
        thresholds: [70, 90] as [number, number],
      },
      {
        value: vramPct,
        label: hasMultiGpu ? "Max VRAM" : "VRAM",
        unit: "%",
        thresholds: [80, 95] as [number, number],
      },
      {
        value: gpuTemp,
        label: hasMultiGpu ? "Max GPU Temp" : "GPU Temp",
        unit: "°C",
        thresholds: [78, 86] as [number, number],
      }
    );
  }

  if (diskPct > 0) {
    mainGauges.push({
      value: diskPct,
      label: "Disk",
      unit: "%",
      thresholds: [85, 95] as [number, number],
    });
  }

  return (
    <div className="space-y-6">
      {/* Main gauges */}
      <div className="bg-surface-raised border border-border-subtle rounded-xl p-6">
        <h3 className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-6">
          Live Metrics
        </h3>
        <div className="flex flex-wrap items-center justify-center gap-6">
          {mainGauges.map((gauge, i) => (
            <Gauge key={i} {...gauge} size="md" />
          ))}
        </div>
      </div>

      {/* Multi-GPU mini gauges */}
      {hasMultiGpu && (
        <div className="bg-surface-raised border border-border-subtle rounded-xl p-5">
          <h3 className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-4">
            Per-GPU Metrics
          </h3>
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
            {gpus.map((gpu) => {
              const vramPct =
                (gpu.mem_total_bytes ?? 0) > 0
                  ? ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100
                  : 0;

              return (
                <div
                  key={gpu.index}
                  className="bg-surface-sunken border border-border-subtle rounded-lg p-3"
                >
                  <div className="text-[10px] font-mono text-text-tertiary mb-2 uppercase tracking-wider">
                    GPU {gpu.index}
                  </div>
                  <div className="space-y-2">
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-text-tertiary uppercase">Util</span>
                      <span className="text-sm font-mono tabular-nums text-text-primary">
                        {gpu.util_percent.toFixed(0)}%
                      </span>
                    </div>
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-text-tertiary uppercase">VRAM</span>
                      <span className="text-sm font-mono tabular-nums text-text-primary">
                        {vramPct.toFixed(0)}%
                      </span>
                    </div>
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-text-tertiary uppercase">Temp</span>
                      <span className="text-sm font-mono tabular-nums text-text-primary">
                        {gpu.temp_c.toFixed(0)}°C
                      </span>
                    </div>
                    {gpu.power_watts > 0 && (
                      <div className="flex justify-between items-baseline">
                        <span className="text-[9px] text-text-tertiary uppercase">Power</span>
                        <span className="text-sm font-mono tabular-nums text-text-primary">
                          {gpu.power_watts.toFixed(0)}W
                        </span>
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
