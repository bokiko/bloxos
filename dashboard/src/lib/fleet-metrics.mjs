/** @param {import("./demo-data").MachineMetrics} machine */
export function hasGpuData(machine) {
  return (machine.gpus?.length ?? 0) > 0 ||
    (machine.gpu_vram_total_bytes ?? 0) > 0 ||
    (machine.gpu_temp ?? 0) > 0 || (machine.gpu_util_percent ?? 0) > 0;
}

export const METRICS_STALE_MS = 30_000;

// OFFLINE_MS is the shared no-heartbeat cutoff (fleet classifyMachine and the
// machine detail page both use it — do not fork the threshold).
export const OFFLINE_MS = 120_000;

const STATUS_TH = {
  cpuWarn: 75,
  cpuCrit: 92,
  ramWarn: 82,
  ramCrit: 95,
  diskWarn: 85,
  diskCrit: 95,
  gpuWarn: 78,
  gpuCrit: 86,
};

// classifyMachine is the single source of truth for fleet status, used by the
// machine card, the overview fleet table and the status filter (re-exported
// by StatusBadge.tsx for existing imports). Freshness outranks thresholds: data
// older than METRICS_STALE_MS is stale, not warning/critical — a badge based
// on aged readings would overstate what we know.
//
// EXPECTED HIGH LOAD
// Some machines are meant to sit at 100% CPU: a miner, a renderer, a training
// box. For those, CPU saturation is the machine doing its job, not an
// incident, and classifying it `critical` makes the one correctly-working
// machine the loudest thing on the dashboard. `options.expectedHighCpu`
// suppresses the CPU rule for ONE machine and nothing else — offline, stale,
// RAM, disk and GPU temperature all still escalate exactly as before, because
// those are genuinely different problems that a busy CPU says nothing about.
//
// The suppression is never silent: when the CPU rule would have fired, the
// classification carries `suppressed` with the reading it swallowed, so the UI
// can mark the machine instead of claiming it is plainly nominal.
/**
 * @param {import("./demo-data").MachineMetrics} m
 * @param {{ expectedHighCpu?: boolean }} [options]
 * @returns {import("../components/StatusBadge").MachineClassification}
 */
export function classifyMachine(m, options) {
  const age = Date.now() - (m.last_seen || 0);
  if (!m.last_seen || age > OFFLINE_MS) return { status: "offline" };
  if (age > METRICS_STALE_MS) return { status: "stale" };

  const ramPct =
    (m.ram_total_bytes ?? 0) > 0
      ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100
      : 0;
  const diskPct =
    (m.disk_total_bytes ?? 0) > 0
      ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100
      : 0;
  const cpu = m.cpu_percent ?? 0;
  const gpuT = m.gpu_temp ?? 0;

  const expectedHighCpu = options?.expectedHighCpu === true;
  // Recorded whenever the flag actually swallows a reading the fleet would
  // otherwise have escalated; absent when the machine is simply not busy.
  const carry =
    expectedHighCpu && cpu >= STATUS_TH.cpuWarn ? { suppressed: `CPU ${cpu.toFixed(0)}%` } : null;
  const out = (status, reason) => ({ status, ...(reason ? { reason } : null), ...carry });

  if (!expectedHighCpu && cpu >= STATUS_TH.cpuCrit) return out("critical", `CPU ${cpu.toFixed(0)}%`);
  if (ramPct >= STATUS_TH.ramCrit) return out("critical", `RAM ${ramPct.toFixed(0)}%`);
  if (diskPct >= STATUS_TH.diskCrit) return out("critical", `Disk ${diskPct.toFixed(0)}%`);
  if (gpuT >= STATUS_TH.gpuCrit) return out("critical", `GPU ${gpuT.toFixed(0)}°C`);

  if (!expectedHighCpu && cpu >= STATUS_TH.cpuWarn) return out("warning", `CPU ${cpu.toFixed(0)}%`);
  if (ramPct >= STATUS_TH.ramWarn) return out("warning", `RAM ${ramPct.toFixed(0)}%`);
  if (diskPct >= STATUS_TH.diskWarn) return out("warning", `Disk ${diskPct.toFixed(0)}%`);
  if (gpuT >= STATUS_TH.gpuWarn) return out("warning", `GPU ${gpuT.toFixed(0)}°C`);

  return out("live");
}

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
