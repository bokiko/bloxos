"use client";

import { useMemo } from "react";
import { motion } from "framer-motion";
import { useSSE } from "@/contexts/SSEContext";
import type { MachineMetrics } from "@/lib/demo-data";
import { Cpu, MemoryStick, Thermometer, AlertTriangle, WifiOff } from "lucide-react";

type Severity = "neutral" | "success" | "warning" | "danger";

interface FleetStats {
  total: number;
  online: number;
  warning: number;
  offline: number;
  activeMachines: number; // online + warning
  avgCpu: number;
  avgRamPct: number;
  maxGpuTemp: number;
  hasGpu: boolean;
}

function classifyMachine(m: MachineMetrics): "online" | "warning" | "offline" {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return "offline";
  if (m.gpu_temp && m.gpu_temp > 80) return "warning";
  const diskPct = (m.disk_total_bytes ?? 0) > 0
    ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100
    : 0;
  if (diskPct > 90) return "warning";
  const ramPct = (m.ram_total_bytes ?? 0) > 0
    ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100
    : 0;
  if (ramPct > 95) return "warning";
  return "online";
}

function computeStats(machines: MachineMetrics[]): FleetStats {
  let online = 0;
  let warning = 0;
  let offline = 0;
  let cpuSum = 0;
  let cpuCount = 0;
  let ramPctSum = 0;
  let ramCount = 0;
  let maxGpuTemp = 0;
  let hasGpu = false;

  for (const m of machines) {
    const status = classifyMachine(m);
    if (status === "online") online++;
    else if (status === "warning") warning++;
    else offline++;

    // Only aggregate active machines into metric averages.
    if (status === "offline") continue;

    if ((m.cpu_percent ?? 0) > 0) {
      cpuSum += m.cpu_percent;
      cpuCount++;
    }
    if ((m.ram_total_bytes ?? 0) > 0) {
      ramPctSum += ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100;
      ramCount++;
    }
    if ((m.gpu_temp ?? 0) > 0) {
      hasGpu = true;
      if ((m.gpu_temp ?? 0) > maxGpuTemp) maxGpuTemp = m.gpu_temp ?? 0;
    }
  }

  return {
    total: machines.length,
    online,
    warning,
    offline,
    activeMachines: online + warning,
    avgCpu: cpuCount > 0 ? cpuSum / cpuCount : 0,
    avgRamPct: ramCount > 0 ? ramPctSum / ramCount : 0,
    maxGpuTemp,
    hasGpu,
  };
}

interface FleetPulseProps {
  onAlertsClick?: () => void;
}

export function FleetPulse({ onAlertsClick }: FleetPulseProps) {
  const { machines, alertCount, alerts, connected, hasReceivedData } = useSSE();
  const stats = useMemo(() => computeStats(machines), [machines]);

  const fleetSeverity: Severity =
    stats.offline > 0 ? "danger" : stats.warning > 0 ? "warning" : stats.online > 0 ? "success" : "neutral";

  const cpuSeverity: Severity =
    stats.avgCpu > 90 ? "danger" : stats.avgCpu > 70 ? "warning" : "neutral";

  const ramSeverity: Severity =
    stats.avgRamPct > 95 ? "danger" : stats.avgRamPct > 80 ? "warning" : "neutral";

  const gpuSeverity: Severity =
    stats.maxGpuTemp > 80 ? "danger" : stats.maxGpuTemp > 60 ? "warning" : "success";

  const criticalAlerts = alerts.filter((a) => a.severity === "critical").length;
  const alertSeverity: Severity =
    criticalAlerts > 0 ? "danger" : alertCount > 0 ? "warning" : "neutral";

  // Skeleton state during initial fetch.
  if (!hasReceivedData && machines.length === 0) {
    return (
      <div className="border-b border-blox-border bg-blox-bg/30">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-3">
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-px bg-blox-border rounded-lg overflow-hidden border border-blox-border">
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="bg-blox-card p-3 h-[68px]">
                <div className="h-2 bg-blox-border/60 rounded w-12 mb-2 animate-shimmer" />
                <div className="h-5 bg-blox-border/60 rounded w-20 animate-shimmer" />
              </div>
            ))}
          </div>
        </div>
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, delay: 0.05 }}
      className="border-b border-blox-border bg-blox-bg/30"
    >
      <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-3">
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-px bg-blox-border rounded-lg overflow-hidden border border-blox-border">

          {/* 1. Fleet Status */}
          <PulseCell label="Fleet" severity={fleetSeverity}>
            <div className="flex items-baseline gap-1.5">
              <span className="text-xl font-semibold text-blox-text tabular-nums">
                {stats.online}
              </span>
              <span className="text-xs text-blox-muted tabular-nums">
                / {stats.total} online
              </span>
            </div>
            <FleetRatioBar
              online={stats.online}
              warning={stats.warning}
              offline={stats.offline}
              total={stats.total}
            />
          </PulseCell>

          {/* 2. Avg CPU */}
          <PulseCell label="Avg CPU" severity={cpuSeverity} icon={<Cpu className="w-3 h-3" />}>
            <div className="flex items-baseline gap-1">
              <span className={`text-xl font-semibold tabular-nums ${severityTextClass(cpuSeverity)}`}>
                {stats.avgCpu.toFixed(1)}
              </span>
              <span className="text-xs text-blox-muted">%</span>
            </div>
            <div className="text-[10px] text-blox-muted mt-1.5 tabular-nums">
              {stats.activeMachines > 0
                ? `across ${stats.activeMachines} machine${stats.activeMachines === 1 ? "" : "s"}`
                : "no active machines"}
            </div>
          </PulseCell>

          {/* 3. Avg RAM */}
          <PulseCell label="Avg RAM" severity={ramSeverity} icon={<MemoryStick className="w-3 h-3" />}>
            <div className="flex items-baseline gap-1">
              <span className={`text-xl font-semibold tabular-nums ${severityTextClass(ramSeverity)}`}>
                {stats.avgRamPct.toFixed(0)}
              </span>
              <span className="text-xs text-blox-muted">%</span>
            </div>
            <SeverityBar pct={stats.avgRamPct} severity={ramSeverity} />
          </PulseCell>

          {/* 4. Max GPU Temp — only when at least one machine has a GPU */}
          {stats.hasGpu ? (
            <PulseCell label="Max GPU" severity={gpuSeverity} icon={<Thermometer className="w-3 h-3" />}>
              <div className="flex items-baseline gap-1">
                <span className={`text-xl font-semibold tabular-nums ${severityTextClass(gpuSeverity)}`}>
                  {stats.maxGpuTemp.toFixed(0)}
                </span>
                <span className="text-xs text-blox-muted">°C</span>
              </div>
              <div className="text-[10px] text-blox-muted mt-1.5">
                {gpuSeverity === "danger"
                  ? "critical"
                  : gpuSeverity === "warning"
                  ? "warm"
                  : "cool"}
              </div>
            </PulseCell>
          ) : (
            // Placeholder cell so the grid keeps 5 columns at lg.
            // Hidden below lg so the grid wraps cleanly to 3 columns.
            <PulseCell label="GPU" severity="neutral" className="hidden lg:flex">
              <div className="flex items-baseline gap-1">
                <span className="text-xl font-semibold tabular-nums text-blox-muted">—</span>
              </div>
              <div className="text-[10px] text-blox-muted mt-1.5">no GPU machines</div>
            </PulseCell>
          )}

          {/* 5. Alerts (clickable to open alert panel) + live status */}
          <PulseCell
            label="Alerts"
            severity={alertSeverity}
            icon={<AlertTriangle className="w-3 h-3" />}
            onClick={alertCount > 0 ? onAlertsClick : undefined}
          >
            <div className="flex items-baseline gap-2">
              <span className={`text-xl font-semibold tabular-nums ${severityTextClass(alertSeverity)}`}>
                {alertCount}
              </span>
              {criticalAlerts > 0 && (
                <span className="text-[10px] text-red-400 font-medium tabular-nums">
                  {criticalAlerts} critical
                </span>
              )}
            </div>
            <div className="flex items-center gap-1.5 text-[10px] text-blox-muted mt-1.5">
              {connected ? (
                <>
                  <span className="relative flex h-1.5 w-1.5">
                    <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                    <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500" />
                  </span>
                  <span>live updates</span>
                </>
              ) : (
                <>
                  <WifiOff className="w-2.5 h-2.5 text-amber-400" />
                  <span className="text-amber-400">reconnecting…</span>
                </>
              )}
            </div>
          </PulseCell>
        </div>
      </div>
    </motion.div>
  );
}

/* ============================================================================
 * Subcomponents
 * ============================================================================ */

function severityTextClass(severity: Severity): string {
  switch (severity) {
    case "danger":
      return "text-red-400";
    case "warning":
      return "text-amber-400";
    case "success":
      return "text-emerald-400";
    case "neutral":
    default:
      return "text-blox-text";
  }
}

interface PulseCellProps {
  label: string;
  severity: Severity;
  icon?: React.ReactNode;
  onClick?: () => void;
  className?: string;
  children: React.ReactNode;
}

function PulseCell({ label, severity, icon, onClick, className, children }: PulseCellProps) {
  const isInteractive = !!onClick;

  // Subtle severity hint via a 2px left accent — only for warning/danger.
  // Avoids painting healthy cells with color.
  const accent =
    severity === "danger"
      ? "before:bg-red-500/70"
      : severity === "warning"
      ? "before:bg-amber-500/70"
      : "before:bg-transparent";

  if (isInteractive) {
    return (
      <button
        type="button"
        onClick={onClick}
        className={[
          "bg-blox-card p-3 text-left relative",
          "before:absolute before:left-0 before:top-2 before:bottom-2 before:w-[2px] before:rounded-full",
          accent,
          "hover:bg-blox-border/30 cursor-pointer transition-colors duration-[var(--motion-fast)]",
          className ?? "",
        ].join(" ")}
      >
        <div className="flex items-center gap-1.5 mb-1">
          {icon && <span className="text-blox-muted shrink-0">{icon}</span>}
          <span className="text-[10px] uppercase tracking-wider font-medium text-blox-muted">
            {label}
          </span>
        </div>
        {children}
      </button>
    );
  }

  return (
    <div
      className={[
        "bg-blox-card p-3 text-left relative",
        "before:absolute before:left-0 before:top-2 before:bottom-2 before:w-[2px] before:rounded-full",
        accent,
        className ?? "",
      ].join(" ")}
    >
      <div className="flex items-center gap-1.5 mb-1">
        {icon && <span className="text-blox-muted shrink-0">{icon}</span>}
        <span className="text-[10px] uppercase tracking-wider font-medium text-blox-muted">
          {label}
        </span>
      </div>
      {children}
    </div>
  );
}

interface FleetRatioBarProps {
  online: number;
  warning: number;
  offline: number;
  total: number;
}

function FleetRatioBar({ online, warning, offline, total }: FleetRatioBarProps) {
  if (total === 0) {
    return <div className="h-1 mt-1.5 rounded-full bg-blox-border/30" />;
  }
  const onlinePct = (online / total) * 100;
  const warningPct = (warning / total) * 100;
  const offlinePct = (offline / total) * 100;

  return (
    <div className="flex h-1 mt-1.5 rounded-full overflow-hidden bg-blox-border/30">
      {online > 0 && (
        <div
          className="bg-emerald-500 transition-all duration-500"
          style={{ width: `${onlinePct}%` }}
          title={`${online} online`}
        />
      )}
      {warning > 0 && (
        <div
          className="bg-amber-500 transition-all duration-500"
          style={{ width: `${warningPct}%` }}
          title={`${warning} warning`}
        />
      )}
      {offline > 0 && (
        <div
          className="bg-red-500/60 transition-all duration-500"
          style={{ width: `${offlinePct}%` }}
          title={`${offline} offline`}
        />
      )}
    </div>
  );
}

interface SeverityBarProps {
  pct: number;
  severity: Severity;
}

function SeverityBar({ pct, severity }: SeverityBarProps) {
  const color =
    severity === "danger"
      ? "bg-red-500"
      : severity === "warning"
      ? "bg-amber-500"
      : "bg-blox-blue/60";

  return (
    <div className="h-1 mt-1.5 rounded-full bg-blox-border/30 overflow-hidden">
      <div
        className={`h-full rounded-full transition-all duration-500 ${color}`}
        style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
      />
    </div>
  );
}
