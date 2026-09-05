"use client";

import { useMemo } from "react";
import { motion } from "framer-motion";
import { useRouter } from "next/navigation";
import { useSSE } from "@/contexts/SSEContext";
import { useAISessions } from "@/contexts/AISessionsContext";
import type { MachineMetrics } from "@/lib/demo-data";
import { summarize } from "@/lib/ai-sessions";
import { Cpu, MemoryStick, Thermometer, AlertTriangle, Activity, WifiOff, Bot } from "lucide-react";
import { classifyMachine } from "@/components/StatusBadge";

/* ============================================================================
 * FleetPulse — top-of-page health strip.
 *
 * One hero cell (FLEET) + five supporting cells (CPU / RAM / GPU / AI / ALERTS).
 * Every cell uses the same locked anatomy via <StatCell>:
 *
 *     ┌───────────────────────────────────────┐
 *     │ LABEL · ICON                          │  ← row 1 (xs uppercase muted)
 *     │ <primary value><unit>                 │  ← row 2 (mono, scale-1.2 in hero)
 *     │ <context line>                        │  ← row 3 (xs muted, tabular nums)
 *     └───────────────────────────────────────┘
 *
 * State tinting (warning/critical) is applied as a subtle background tint,
 * never as a colored left-border. Healthy cells stay neutral so problem
 * cells pop without competing visual weight.
 * ============================================================================ */

type Severity = "neutral" | "ok" | "warning" | "critical";

interface FleetStats {
  total: number;
  live: number;
  warning: number;
  critical: number;
  offline: number;
  stale: number;
  activeMachines: number; // live + warning + critical + stale
  avgCpu: number;
  avgRamPct: number;
  maxGpuTemp: number;
  hasGpu: boolean;
}

function computeStats(machines: MachineMetrics[]): FleetStats {
  let live = 0;
  let warning = 0;
  let critical = 0;
  let offline = 0;
  let stale = 0;
  let cpuSum = 0;
  let cpuCount = 0;
  let ramPctSum = 0;
  let ramCount = 0;
  let maxGpuTemp = 0;
  let hasGpu = false;

  for (const m of machines) {
    const { status } = classifyMachine(m);
    if (status === "live") live++;
    else if (status === "warning") warning++;
    else if (status === "critical") critical++;
    else if (status === "stale") stale++;
    else offline++;

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
    live,
    warning,
    critical,
    offline,
    stale,
    activeMachines: live + warning + critical + stale,
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
  const router = useRouter();
  const ai = useAISessions();
  const aiTotals = useMemo(() => summarize(ai.machines.values()), [ai.machines]);

  // Fleet severity = aggregate of the worst state present.
  const fleetSeverity: Severity =
    stats.critical > 0 || stats.offline > 0
      ? "critical"
      : stats.warning > 0 || stats.stale > 0
      ? "warning"
      : stats.live > 0
      ? "ok"
      : "neutral";

  const cpuSeverity: Severity = stats.avgCpu >= 90 ? "critical" : stats.avgCpu >= 70 ? "warning" : "neutral";
  const ramSeverity: Severity = stats.avgRamPct >= 95 ? "critical" : stats.avgRamPct >= 80 ? "warning" : "neutral";
  const gpuSeverity: Severity =
    stats.maxGpuTemp >= 85 ? "critical" : stats.maxGpuTemp >= 70 ? "warning" : "neutral";

  const criticalAlerts = alerts.filter((a) => a.severity === "critical").length;
  const alertSeverity: Severity =
    criticalAlerts > 0 ? "critical" : alertCount > 0 ? "warning" : "neutral";

  // Skeleton: real layout shape, with subtle shimmer on the value rows so
  // the loaded UI does not "pop" in. Never an empty card.
  if (!hasReceivedData && machines.length === 0) {
    return (
      <div className="border-b border-border-subtle bg-surface-base">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-3">
          <StatRow>
            {Array.from({ length: 6 }).map((_, i) => (
              <StatCell
                key={i}
                label="…"
                severity="neutral"
                hero={i === 0}
                primary={<span className="metric-value text-[1.1em] text-text-disabled">— —</span>}
                context={<span className="h-2 w-24 rounded-sm bg-border-subtle animate-shimmer block" />}
              />
            ))}
          </StatRow>
        </div>
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.2 }}
      className="border-b border-border-subtle bg-surface-base"
    >
      <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-3">
        <StatRow>
          {/* 1 · FLEET (hero) ─────────────────────────────────────────── */}
          <StatCell
            label="Fleet"
            icon={<Activity className="h-3 w-3" />}
            severity={fleetSeverity}
            hero
            primary={
              <span className="inline-flex items-baseline gap-1.5">
                <span className="metric-value text-3xl text-text-primary">{stats.live}</span>
                <span className="metric-unit text-sm">/ {stats.total}</span>
              </span>
            }
            context={
              <FleetMix
                live={stats.live}
                warning={stats.warning}
                critical={stats.critical}
                stale={stats.stale}
                offline={stats.offline}
                total={stats.total}
              />
            }
          />

          {/* 2 · CPU ─────────────────────────────────────────────────── */}
          <StatCell
            label="Avg CPU"
            icon={<Cpu className="h-3 w-3" />}
            severity={cpuSeverity}
            primary={
              <span className="inline-flex items-baseline">
                <span className={`metric-value text-2xl ${sevText(cpuSeverity)}`}>
                  {stats.avgCpu.toFixed(1)}
                </span>
                <span className="metric-unit text-xs ml-1">%</span>
              </span>
            }
            context={
              <span className="metric-figure text-text-tertiary text-[11px]">
                across {stats.activeMachines || 0} active
              </span>
            }
          />

          {/* 3 · RAM ─────────────────────────────────────────────────── */}
          <StatCell
            label="Avg RAM"
            icon={<MemoryStick className="h-3 w-3" />}
            severity={ramSeverity}
            primary={
              <span className="inline-flex items-baseline">
                <span className={`metric-value text-2xl ${sevText(ramSeverity)}`}>
                  {stats.avgRamPct.toFixed(0)}
                </span>
                <span className="metric-unit text-xs ml-1">%</span>
              </span>
            }
            context={<SeverityRail pct={stats.avgRamPct} severity={ramSeverity} />}
          />

          {/* 4 · GPU ─────────────────────────────────────────────────── */}
          {stats.hasGpu ? (
            <StatCell
              label="Max GPU"
              icon={<Thermometer className="h-3 w-3" />}
              severity={gpuSeverity}
              primary={
                <span className="inline-flex items-baseline">
                  <span className={`metric-value text-2xl ${sevText(gpuSeverity)}`}>
                    {stats.maxGpuTemp.toFixed(0)}
                  </span>
                  <span className="metric-unit text-xs ml-1">°C</span>
                </span>
              }
              context={
                <span className="metric-figure text-text-tertiary text-[11px] uppercase tracking-[0.08em]">
                  {gpuSeverity === "critical" ? "thermal critical" : gpuSeverity === "warning" ? "warm" : "nominal"}
                </span>
              }
            />
          ) : (
            <StatCell
              label="GPU"
              icon={<Thermometer className="h-3 w-3" />}
              severity="neutral"
              className="hidden lg:flex"
              primary={<span className="metric-value text-2xl text-text-disabled">—</span>}
              context={<span className="metric-figure text-text-tertiary text-[11px]">no GPU machines</span>}
            />
          )}

          {/* 5 · AI SESSIONS ─────────────────────────────────────────── */}
          <StatCell
            label="AI Sessions"
            icon={<Bot className="h-3 w-3" />}
            severity="neutral"
            onClick={() => router.push("/sessions")}
            className="hidden md:flex"
            primary={
              ai.enabled === false ? (
                <span className="metric-value text-2xl text-text-disabled">off</span>
              ) : !ai.hasLoaded ? (
                <span className="metric-value text-2xl text-text-disabled">—</span>
              ) : (
                <span className="inline-flex items-baseline gap-1.5">
                  <span className="metric-value text-2xl text-text-primary">{aiTotals.sessions}</span>
                  <span className="metric-unit text-xs">
                    on {aiTotals.machines} machine{aiTotals.machines === 1 ? "" : "s"}
                  </span>
                </span>
              )
            }
            context={
              <span className="metric-figure text-text-tertiary text-[11px]">
                {ai.enabled === false
                  ? "disabled by admin"
                  : !ai.hasLoaded
                    ? "loading"
                    : aiTotals.sessions === 0
                      ? "no coding sessions"
                      : "running now · live"}
              </span>
            }
          />

          {/* 6 · ALERTS ─────────────────────────────────────────────── */}
          <StatCell
            label="Alerts"
            icon={<AlertTriangle className="h-3 w-3" />}
            severity={alertSeverity}
            onClick={alertCount > 0 ? onAlertsClick : undefined}
            primary={
              <span className="inline-flex items-baseline gap-2">
                <span className={`metric-value text-2xl ${sevText(alertSeverity)}`}>{alertCount}</span>
                {criticalAlerts > 0 && (
                  <span className="metric-figure text-status-critical text-[10px] uppercase tracking-[0.08em]">
                    {criticalAlerts} crit
                  </span>
                )}
              </span>
            }
            context={
              <span className="flex items-center gap-1.5 text-[11px] text-text-tertiary">
                {connected ? (
                  <>
                    <span className="relative inline-flex h-1.5 w-1.5">
                      <span className="absolute inset-0 rounded-full bg-status-ok opacity-60 animate-status-pulse" />
                      <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-status-ok" />
                    </span>
                    <span>live</span>
                  </>
                ) : (
                  <>
                    <WifiOff className="h-2.5 w-2.5 text-status-warning" />
                    <span className="text-status-warning">reconnecting</span>
                  </>
                )}
              </span>
            }
          />
        </StatRow>
      </div>
    </motion.div>
  );
}

/* ============================================================================
 * Layout — asymmetric grid. Hero claims 5fr, supporting cells 3fr each. On
 * mobile the row collapses to 2-col; the AI cell hides below md so the
 * strip never exceeds one row of five on tablets.
 * ============================================================================ */

function StatRow({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="grid grid-cols-2 sm:grid-cols-[5fr_3fr_3fr_3fr_3fr] md:grid-cols-[5fr_3fr_3fr_3fr_3fr_3fr] gap-px overflow-hidden rounded-lg bg-border-subtle ring-1 ring-border-subtle"
    >
      {children}
    </div>
  );
}

/* ============================================================================
 * StatCell — locked anatomy. Every consumer of the strip uses this.
 * ============================================================================ */

function sevText(s: Severity): string {
  return s === "critical"
    ? "text-status-critical"
    : s === "warning"
    ? "text-status-warning"
    : s === "ok"
    ? "text-text-primary"
    : "text-text-primary";
}

function sevSurface(s: Severity): string {
  return s === "critical"
    ? "bg-status-critical-tint"
    : s === "warning"
    ? "bg-status-warning-tint"
    : "bg-surface-raised";
}

interface StatCellProps {
  label: string;
  icon?: React.ReactNode;
  severity: Severity;
  hero?: boolean;
  className?: string;
  onClick?: () => void;
  primary: React.ReactNode;
  context: React.ReactNode;
}

function StatCell({ label, icon, severity, hero, className, onClick, primary, context }: StatCellProps) {
  const interactive = !!onClick;
  const body = (
    <>
      <div className="flex items-center gap-1.5 text-text-tertiary">
        {icon && <span className="shrink-0">{icon}</span>}
        <span className="text-[10px] font-medium uppercase tracking-[0.14em]">{label}</span>
      </div>
      <div className={`${hero ? "mt-1.5" : "mt-1"} leading-none`}>{primary}</div>
      <div className="mt-2">{context}</div>
    </>
  );

  const base = [
    "relative flex flex-col px-3.5 py-3 text-left",
    sevSurface(severity),
    "transition-colors duration-[var(--motion-fast)]",
    hero ? "min-h-[88px]" : "min-h-[82px]",
    className ?? "",
  ].join(" ");

  if (interactive) {
    return (
      <button type="button" onClick={onClick} className={`${base} cursor-pointer hover:bg-surface-elevated`}>
        {body}
      </button>
    );
  }
  return <div className={base}>{body}</div>;
}

/* ============================================================================
 * FleetMix — segmented bar showing live / warn / critical / stale / offline
 * ratio. Replaces the old 3-color bar so all 5 states are visible.
 * ============================================================================ */

function FleetMix({
  live,
  warning,
  critical,
  stale,
  offline,
  total,
}: {
  live: number;
  warning: number;
  critical: number;
  stale: number;
  offline: number;
  total: number;
}) {
  if (total === 0) {
    return (
      <span className="metric-figure text-text-tertiary text-[11px]">no machines enrolled</span>
    );
  }
  const seg = (n: number) => `${(n / total) * 100}%`;
  return (
    <div className="space-y-1.5">
      <div className="flex h-[3px] overflow-hidden rounded-full bg-surface-sunken">
        {critical > 0 && <div className="bg-status-critical transition-[width] duration-[var(--motion-slow)]" style={{ width: seg(critical) }} title={`${critical} critical`} />}
        {warning > 0 && <div className="bg-status-warning transition-[width] duration-[var(--motion-slow)]" style={{ width: seg(warning) }} title={`${warning} warning`} />}
        {stale > 0 && <div className="bg-status-stale transition-[width] duration-[var(--motion-slow)]" style={{ width: seg(stale) }} title={`${stale} stale`} />}
        {offline > 0 && <div className="bg-status-offline transition-[width] duration-[var(--motion-slow)]" style={{ width: seg(offline) }} title={`${offline} offline`} />}
        {live > 0 && <div className="bg-status-ok transition-[width] duration-[var(--motion-slow)]" style={{ width: seg(live) }} title={`${live} live`} />}
      </div>
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[10px] font-medium uppercase tracking-[0.06em]">
        {critical > 0 && <MixLegend n={critical} label="crit" color="text-status-critical" />}
        {warning > 0 && <MixLegend n={warning} label="warn" color="text-status-warning" />}
        {offline > 0 && <MixLegend n={offline} label="off" color="text-status-offline" />}
        {stale > 0 && <MixLegend n={stale} label="stale" color="text-status-stale" />}
      </div>
    </div>
  );
}

function MixLegend({ n, label, color }: { n: number; label: string; color: string }) {
  return (
    <span className={color}>
      <span className="metric-figure">{n}</span>
      <span className="ml-0.5 text-text-tertiary">{label}</span>
    </span>
  );
}

/* ============================================================================
 * SeverityRail — single horizontal indicator used by RAM cell.
 * ============================================================================ */

function SeverityRail({ pct, severity }: { pct: number; severity: Severity }) {
  const fill =
    severity === "critical" ? "bg-status-critical" : severity === "warning" ? "bg-status-warning" : "bg-accent";
  return (
    <div className="h-[3px] overflow-hidden rounded-full bg-surface-sunken">
      <div
        className={`h-full rounded-full ${fill} transition-[width] duration-[var(--motion-slow)] ease-out`}
        style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
      />
    </div>
  );
}
