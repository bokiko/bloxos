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
  const hasSingleGpu =
    (gpus && gpus.length === 1) ||
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

  // Single GPU or aggregated GPU metrics
  if (hasSingleGpu) {
    const gpu = gpus?.[0];
    const gpuUtil = gpu ? gpu.util_percent : metrics.gpu_util_percent ?? 0;
    const gpuTemp = gpu ? gpu.temp_c : metrics.gpu_temp ?? 0;
    const vramPct =
      gpu && (gpu.mem_total_bytes ?? 0) > 0
        ? ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100
        : (metrics.gpu_vram_total_bytes ?? 0) > 0
        ? ((metrics.gpu_vram_used_bytes ?? 0) / metrics.gpu_vram_total_bytes) * 100
        : 0;

    mainGauges.push(
      {
        value: gpuUtil,
        label: "GPU Util",
        unit: "%",
        thresholds: [70, 90] as [number, number],
      },
      {
        value: vramPct,
        label: "VRAM",
        unit: "%",
        thresholds: [80, 95] as [number, number],
      },
      {
        value: gpuTemp,
        label: "GPU Temp",
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
      <div className="bg-blox-card border border-blox-border rounded-xl p-6">
        <h3 className="text-xs font-medium text-blox-muted uppercase tracking-wider mb-6">
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
        <div className="bg-blox-card border border-blox-border rounded-xl p-5">
          <h3 className="text-xs font-medium text-blox-muted uppercase tracking-wider mb-4">
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
                  className="bg-blox-bg/50 border border-blox-border/50 rounded-lg p-3"
                >
                  <div className="text-[10px] font-mono text-blox-muted mb-2 uppercase tracking-wider">
                    GPU {gpu.index}
                  </div>
                  <div className="space-y-2">
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-blox-muted uppercase">Util</span>
                      <span className="text-sm font-mono tabular-nums text-blox-text">
                        {gpu.util_percent.toFixed(0)}%
                      </span>
                    </div>
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-blox-muted uppercase">VRAM</span>
                      <span className="text-sm font-mono tabular-nums text-blox-text">
                        {vramPct.toFixed(0)}%
                      </span>
                    </div>
                    <div className="flex justify-between items-baseline">
                      <span className="text-[9px] text-blox-muted uppercase">Temp</span>
                      <span className="text-sm font-mono tabular-nums text-blox-text">
                        {gpu.temp_c.toFixed(0)}°C
                      </span>
                    </div>
                    {gpu.power_watts > 0 && (
                      <div className="flex justify-between items-baseline">
                        <span className="text-[9px] text-blox-muted uppercase">Power</span>
                        <span className="text-sm font-mono tabular-nums text-blox-text">
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
