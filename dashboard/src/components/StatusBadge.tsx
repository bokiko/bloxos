"use client";

import type { MachineMetrics } from "@/lib/demo-data";
import { METRICS_STALE_MS } from "@/lib/fleet-metrics.mjs";

/* ============================================================================
 * Fleet status — 5 intent states.
 *
 *   live      heartbeat + fresh metrics, all thresholds nominal
 *   stale     heartbeat present but no metric update in 30–120s
 *   warning   one or more metrics in warning zone
 *   critical  one or more metrics in danger zone (urgent operator attention)
 *   offline   no heartbeat in >120s
 *
 * `classifyMachine` is the single source of truth — used by the card, the
 * stat strip, the filter dropdown, and the NeedsAttention stripe. Keep
 * thresholds in sync with the hub-side `evaluateAlerts` rules.
 * ============================================================================ */

export type MachineStatus = "live" | "stale" | "warning" | "critical" | "offline";

const OFFLINE_MS = 120_000;

const TH = {
  cpuWarn: 75,
  cpuCrit: 92,
  ramWarn: 82,
  ramCrit: 95,
  diskWarn: 85,
  diskCrit: 95,
  gpuWarn: 78,
  gpuCrit: 86,
};

export interface MachineClassification {
  status: MachineStatus;
  reason?: string;
}

export function classifyMachine(m: MachineMetrics): MachineClassification {
  const age = Date.now() - (m.last_seen || 0);
  if (!m.last_seen || age > OFFLINE_MS) return { status: "offline" };

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

  if (cpu >= TH.cpuCrit) return { status: "critical", reason: `CPU ${cpu.toFixed(0)}%` };
  if (ramPct >= TH.ramCrit) return { status: "critical", reason: `RAM ${ramPct.toFixed(0)}%` };
  if (diskPct >= TH.diskCrit) return { status: "critical", reason: `Disk ${diskPct.toFixed(0)}%` };
  if (gpuT >= TH.gpuCrit) return { status: "critical", reason: `GPU ${gpuT.toFixed(0)}°C` };

  if (cpu >= TH.cpuWarn) return { status: "warning", reason: `CPU ${cpu.toFixed(0)}%` };
  if (ramPct >= TH.ramWarn) return { status: "warning", reason: `RAM ${ramPct.toFixed(0)}%` };
  if (diskPct >= TH.diskWarn) return { status: "warning", reason: `Disk ${diskPct.toFixed(0)}%` };
  if (gpuT >= TH.gpuWarn) return { status: "warning", reason: `GPU ${gpuT.toFixed(0)}°C` };

  if (age > METRICS_STALE_MS) return { status: "stale" };
  return { status: "live" };
}

/** Ordering for sort-by-status — problem machines first. */
export const STATUS_ORDER: Record<MachineStatus, number> = {
  critical: 0,
  warning: 1,
  offline: 2,
  stale: 3,
  live: 4,
};

/** Operator-attention states surface at the top of the fleet. */
export function isProblem(s: MachineStatus): boolean {
  return s === "critical" || s === "warning" || s === "offline" || s === "stale";
}

/* ============================================================================
 * Visual tokens — color + label for each state.
 * Centralized so the StatCard, MachineCard, and NeedsAttention stripe
 * never drift. Every consumer reads from `STATUS_VIS`.
 * ============================================================================ */

export interface StatusVisual {
  label: string;
  dotClass: string;
  textClass: string;
  surfaceTintClass: string;
  ringClass: string;
}

export const STATUS_VIS: Record<MachineStatus, StatusVisual> = {
  live: {
    label: "Live",
    dotClass: "bg-status-ok",
    textClass: "text-status-ok",
    surfaceTintClass: "",
    ringClass: "ring-status-ok/30",
  },
  stale: {
    label: "Stale",
    dotClass: "bg-status-stale",
    textClass: "text-status-stale",
    surfaceTintClass: "bg-status-stale-tint",
    ringClass: "ring-status-stale/30",
  },
  warning: {
    label: "Warn",
    dotClass: "bg-status-warning",
    textClass: "text-status-warning",
    surfaceTintClass: "bg-status-warning-tint",
    ringClass: "ring-status-warning/35",
  },
  critical: {
    label: "Critical",
    dotClass: "bg-status-critical",
    textClass: "text-status-critical",
    surfaceTintClass: "bg-status-critical-tint",
    ringClass: "ring-status-critical/40",
  },
  offline: {
    label: "Offline",
    dotClass: "bg-status-offline",
    textClass: "text-status-offline",
    surfaceTintClass: "bg-status-offline-tint",
    ringClass: "ring-status-offline/30",
  },
};

/* ============================================================================
 * StatusDot — minimum-viable indicator.
 * Used everywhere a colored dot is enough. Live state gets a soft outer ring
 * pulse to convey "this is actively reporting" without animating its position.
 * ============================================================================ */

interface StatusDotProps {
  status: MachineStatus;
  size?: "xs" | "sm" | "md";
  pulse?: boolean;
}

export function StatusDot({ status, size = "sm", pulse = false }: StatusDotProps) {
  const dim = size === "xs" ? "h-1.5 w-1.5" : size === "md" ? "h-2.5 w-2.5" : "h-2 w-2";
  const v = STATUS_VIS[status];
  return (
    <span className={`relative inline-flex shrink-0 ${dim}`} aria-label={`Status: ${v.label.toLowerCase()}`}>
      {pulse && status === "live" && (
        <span className={`absolute inset-0 rounded-full ${v.dotClass} opacity-50 animate-status-pulse`} />
      )}
      <span className={`relative inline-flex rounded-full ${dim} ${v.dotClass}`} />
    </span>
  );
}

/* ============================================================================
 * StatusBadge — dot + label, used in row contexts (NeedsAttention, machine
 * detail header). Compact, never colored borders, just a tinted pill.
 * ============================================================================ */

interface StatusBadgeProps {
  status: MachineStatus;
  reason?: string;
  size?: "xs" | "sm";
}

export function StatusBadge({ status, reason, size = "sm" }: StatusBadgeProps) {
  const v = STATUS_VIS[status];
  const pad = size === "xs" ? "px-1.5 py-0 text-[10px]" : "px-2 py-0.5 text-[11px]";
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md ${pad} ${v.surfaceTintClass || "bg-surface-elevated"} ${v.textClass} font-medium tracking-tight`}
    >
      <StatusDot status={status} size="xs" pulse={status === "live"} />
      <span>{v.label}</span>
      {reason && (
        <span className="text-text-tertiary font-mono tabular-nums lowercase font-normal">
          · {reason}
        </span>
      )}
    </span>
  );
}
