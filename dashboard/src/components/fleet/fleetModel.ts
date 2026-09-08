// Shared fleet aggregation for the live dashboard layouts (Operations Wall,
// Grove Workspace, Precision Console). Every value is derived from real SSE
// machine data. An aggregate with no underlying readings is `null` and is
// rendered as "N/A" — never a fabricated zero or a false all-healthy state
// (AGENTS.md: missing sensors are unavailable, not zero).
//
// Metrics use FRESH machines only (freshness via the shared isFreshMetrics
// cutoff): stale and offline machines are connected but their readings are
// aged, so they never contribute to averages, temps, power or the rankings.
// The "online" count is the broader connected count (includes stale) and is
// labelled as connected. GPU aggregates prefer per-device `gpus[]` data and
// only fall back to the legacy machine-level fields; CPU-only machines are
// excluded from GPU stats rather than diluting them with zeros. GPU power is
// summed only from devices that report it and flagged partial when not every
// device did — it is never presented as a complete fleet total when partial.

import { classifyMachine, isFreshMetrics, hasGpuData } from "../../lib/fleet-metrics.mjs";
import type { MachineMetrics } from "@/lib/demo-data";
import type { MachineStatus } from "@/components/StatusBadge";

/** A metric that may be unknown/unavailable. null renders as "N/A". */
export type Metric = number | null;

export interface RankRow {
  name: string;
  machineId: string;
  value: number;
}

export interface FleetAggregate {
  total: number;
  /** Connected machines (not offline); includes stale. */
  online: number;
  onlinePct: Metric;
  avgCpu: Metric;
  avgRam: Metric;
  avgGpuUtil: Metric;
  avgVram: Metric;
  maxGpuTemp: Metric;
  gpuPowerTotal: Metric;
  /** True only when every fresh GPU device reported power. Otherwise the
   * total above is a partial sum and must not be labelled a complete total. */
  gpuPowerComplete: boolean;
  topGpu: RankRow[];
  topVram: RankRow[];
}

function mean(nums: number[]): Metric {
  return nums.length ? nums.reduce((a, b) => a + b, 0) / nums.length : null;
}

export function statusOf(m: MachineMetrics): MachineStatus {
  return classifyMachine(m).status as MachineStatus;
}

function finiteOrNull(v: number): Metric {
  return Number.isFinite(v) ? v : null;
}

export function ramPct(m: MachineMetrics): Metric {
  const total = m.ram_total_bytes ?? 0;
  if (!(total > 0)) return null;
  return finiteOrNull(((m.ram_used_bytes ?? 0) / total) * 100);
}

export function diskPct(m: MachineMetrics): Metric {
  const total = m.disk_total_bytes ?? 0;
  if (!(total > 0)) return null;
  return finiteOrNull(((m.disk_used_bytes ?? 0) / total) * 100);
}

/** GPU utilization for one machine — mean across its devices, preferring the
 * per-device gpus[] array. A CPU-only machine (no gpus[] and no other GPU
 * signal) is null even if it reports gpu_util_percent=0, so it never counts
 * as a GPU at zero utilization. */
export function machineGpuUtil(m: MachineMetrics): Metric {
  if (m.gpus && m.gpus.length > 0) {
    const vals = m.gpus.map((g) => g.util_percent).filter((v) => Number.isFinite(v) && v >= 0);
    return vals.length ? mean(vals) : null;
  }
  if (hasGpuData(m) && Number.isFinite(m.gpu_util_percent) && (m.gpu_util_percent as number) >= 0) {
    return m.gpu_util_percent as number;
  }
  return null;
}

/** VRAM usage % for one machine — summed across devices (used/total),
 * preferring gpus[]; null when no VRAM is reported. */
export function machineVramPct(m: MachineMetrics): Metric {
  if (m.gpus && m.gpus.length > 0) {
    let used = 0;
    let total = 0;
    for (const g of m.gpus) {
      if ((g.mem_total_bytes ?? 0) > 0) {
        used += g.mem_used_bytes ?? 0;
        total += g.mem_total_bytes;
      }
    }
    return total > 0 ? finiteOrNull((used / total) * 100) : null;
  }
  const total = m.gpu_vram_total_bytes ?? 0;
  if (!(total > 0)) return null;
  return finiteOrNull(((m.gpu_vram_used_bytes ?? 0) / total) * 100);
}

/** Hottest GPU on one machine, preferring per-device temps; null if none. */
export function machineMaxTemp(m: MachineMetrics): Metric {
  let t: number | null = null;
  if (m.gpus) {
    for (const g of m.gpus) {
      if (Number.isFinite(g.temp_c) && g.temp_c > 0) t = Math.max(t ?? 0, g.temp_c);
    }
  }
  if (t === null && Number.isFinite(m.gpu_temp) && (m.gpu_temp as number) > 0) t = m.gpu_temp as number;
  return t;
}

export function aggregate(machines: MachineMetrics[], now: number = Date.now()): FleetAggregate {
  const valid = machines.filter((m) => m && typeof m.machine_id === "string");
  const online = valid.filter((m) => statusOf(m) !== "offline").length;
  const fresh = valid.filter((m) => isFreshMetrics(m.last_seen, now));

  const cpu = fresh.map((m) => m.cpu_percent).filter((v): v is number => Number.isFinite(v) && v >= 0);
  const ram = fresh.map(ramPct).filter((v): v is number => v !== null);

  // GPU aggregates are sampled PER DEVICE (matching computeFleetMetrics): a
  // machine with two GPUs contributes two samples, so unequal GPU counts
  // weight the fleet average by device rather than by machine. CPU-only
  // machines contribute nothing.
  //
  // GPU power is special: the agent's parseNvValue serializes an unknown/"N/A"
  // reading as 0 (agent/main.go), so on the wire 0 W is indistinguishable from
  // "not reported". Only a strictly positive reading is treated as an
  // observation. A total is therefore reported only when at least one device
  // reported >0; it is complete only when EVERY GPU device did. All-zero or
  // no-reading fleets are unavailable (null), never a false 0 W total, and a
  // mix of positive and zero is a partial sum.
  const gpuUtil: number[] = [];
  const vram: number[] = [];
  const temps: number[] = [];
  let power = 0;
  let powerDevices = 0;
  let gpuDevices = 0;
  for (const m of fresh) {
    if (m.gpus && m.gpus.length > 0) {
      for (const g of m.gpus) {
        gpuDevices += 1;
        if (Number.isFinite(g.util_percent) && g.util_percent >= 0) gpuUtil.push(g.util_percent);
        if ((g.mem_total_bytes ?? 0) > 0) {
          const v = ((g.mem_used_bytes ?? 0) / g.mem_total_bytes) * 100;
          if (Number.isFinite(v)) vram.push(v);
        }
        if (Number.isFinite(g.temp_c) && g.temp_c > 0) temps.push(g.temp_c);
        // Strictly > 0: a 0 may be a real idle draw or an unknown serialized as
        // 0, and the legacy wire cannot tell them apart, so 0 never counts as a
        // power observation.
        if (Number.isFinite(g.power_watts) && g.power_watts > 0) {
          power += g.power_watts;
          powerDevices += 1;
        }
      }
    } else if (hasGpuData(m)) {
      // Legacy machine-level GPU fields (one implied device, no power field).
      gpuDevices += 1;
      if (Number.isFinite(m.gpu_util_percent) && (m.gpu_util_percent as number) >= 0) {
        gpuUtil.push(m.gpu_util_percent as number);
      }
      const vt = m.gpu_vram_total_bytes ?? 0;
      if (vt > 0) {
        const v = ((m.gpu_vram_used_bytes ?? 0) / vt) * 100;
        if (Number.isFinite(v)) vram.push(v);
      }
      if (Number.isFinite(m.gpu_temp) && (m.gpu_temp as number) > 0) temps.push(m.gpu_temp as number);
    }
  }

  const rank = (selector: (m: MachineMetrics) => Metric): RankRow[] =>
    fresh
      .map((m) => ({ name: m.hostname ?? m.machine_id, machineId: m.machine_id, value: selector(m) }))
      .filter((r): r is RankRow => r.value !== null)
      .sort((a, b) => b.value - a.value)
      .slice(0, 3);

  return {
    total: valid.length,
    online,
    onlinePct: valid.length ? (online / valid.length) * 100 : null,
    avgCpu: mean(cpu),
    avgRam: mean(ram),
    avgGpuUtil: mean(gpuUtil),
    avgVram: mean(vram),
    maxGpuTemp: temps.length ? Math.max(...temps) : null,
    gpuPowerTotal: powerDevices > 0 ? power : null,
    gpuPowerComplete: gpuDevices > 0 && powerDevices === gpuDevices,
    topGpu: rank(machineGpuUtil),
    topVram: rank(machineVramPct),
  };
}

export function pct(value: Metric): string {
  return value === null ? "N/A" : `${Math.round(value)}%`;
}

export function pct1(value: Metric): string {
  return value === null ? "N/A" : `${value.toFixed(1)}%`;
}

export function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes === 0) return "0";
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / 1024 ** 2;
  return `${mb.toFixed(0)} MB`;
}

export function timeSince(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

/** The app's semantic status token for a machine, as a CSS var reference for
 * inline `--dot`. Keeps status meaning consistent across every layout. */
export function statusVar(status: MachineStatus): string {
  return `var(--status-${status === "live" ? "ok" : status})`;
}
