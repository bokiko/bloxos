/** @param {import("./demo-data").MachineMetrics} machine */
export function hasGpuData(machine) {
  return (machine.gpus?.length ?? 0) > 0 ||
    (machine.gpu_vram_total_bytes ?? 0) > 0 ||
    (machine.gpu_temp ?? 0) > 0 || (machine.gpu_util_percent ?? 0) > 0;
}

export const METRICS_STALE_MS = 30_000;

/** @param {number | undefined} lastSeen @param {number} now */
export function isFreshMetrics(lastSeen, now) {
  return Number.isFinite(lastSeen) && lastSeen > 0 && now - lastSeen <= METRICS_STALE_MS;
}

/**
 * Aggregate fresh readings only; callers filter with isFreshMetrics first.
 * @param {import("./demo-data").MachineMetrics[]} machines
 */
export function computeFleetMetrics(machines) {
  const activeMachines = machines;

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
    if (Number.isFinite(m.cpu_percent) && m.cpu_percent >= 0) {
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
        if (Number.isFinite(gpu.util_percent) && gpu.util_percent >= 0) {
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
    } else if (hasGpuData(m)) {
      // Fallback to machine-level GPU metrics
      gpuCount++;
      if (Number.isFinite(m.gpu_util_percent) && m.gpu_util_percent >= 0) {
        gpuUtilSum += m.gpu_util_percent ?? 0;
        gpuUtilCount++;
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
