"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { motion } from "framer-motion";
import { Trash2, Pencil, Zap, RefreshCw, Star, Thermometer } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import { ProgressBar, type ProgressVariant } from "./ProgressBar";
import { Sparkline } from "./Sparkline";
import { useVersions } from "@/contexts/VersionsContext";
import { usePreferences } from "@/contexts/PreferencesContext";

/* ============================================================================
 * Helpers
 * ============================================================================ */

function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes === 0) return "0";
  const tb = bytes / 1024 ** 4;
  if (tb >= 1) return `${tb.toFixed(tb >= 100 ? 0 : 1)} TB`;
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(gb >= 100 ? 0 : 1)} GB`;
  const mb = bytes / 1024 ** 2;
  return `${mb.toFixed(0)} MB`;
}

function timeSince(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

type Status = "online" | "warning" | "offline";

function classifyMachine(m: MachineMetrics): { status: Status; reason?: string } {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return { status: "offline" };

  if (m.gpu_temp && m.gpu_temp > 80) {
    return { status: "warning", reason: `GPU ${m.gpu_temp.toFixed(0)}°C` };
  }
  const diskPct =
    (m.disk_total_bytes ?? 0) > 0
      ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100
      : 0;
  if (diskPct > 90) return { status: "warning", reason: `Disk ${diskPct.toFixed(0)}%` };

  const ramPct =
    (m.ram_total_bytes ?? 0) > 0
      ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100
      : 0;
  if (ramPct > 95) return { status: "warning", reason: `RAM ${ramPct.toFixed(0)}%` };

  return { status: "online" };
}

function isAwaitingData(m: MachineMetrics): boolean {
  return (
    (m.cpu_percent ?? 0) === 0 &&
    (m.ram_total_bytes ?? 0) === 0 &&
    (m.disk_total_bytes ?? 0) === 0
  );
}

const API_ADAPTERS = ["synology", "proxmox"];

function deriveMetricVariant(pct: number, warningAt: number, dangerAt: number): ProgressVariant {
  if (pct <= 0) return "neutral";
  if (pct >= dangerAt) return "danger";
  if (pct >= warningAt) return "warning";
  return "active";
}

// PHASE12-NOTE: shared thermal-color thresholds. The existing GPU row
// uses a slightly different palette (emerald-400 below 60°C) so we
// can't trivially reuse this there without changing GPU-row semantics
// — kept as its own helper to avoid touching GPU rendering.
function getTempColorClass(c: number): string {
  if (c >= 80) return "text-red-400";
  if (c >= 60) return "text-amber-400";
  return "text-blox-text";
}

/* ============================================================================
 * Main component
 * ============================================================================ */

interface MachineCardProps {
  machine: MachineMetrics;
  onDelete?: (machineId: string, hostname: string) => void;
  onEdit?: (machineId: string) => void;
  onRefresh?: (machineId: string) => void;
}

export function MachineCard({ machine, onDelete, onEdit, onRefresh }: MachineCardProps) {
  // The footer's "12s ago" timer is owned by <LiveTimeSince/> below — it
  // mounts its own 1Hz ticker, so the parent doesn't need to force re-renders.

  // B9 — was previously wrapped in a <Link>, which produced invalid
  // <a><button>...</button></a> nesting because the action buttons
  // (pin / refresh / edit / delete) live inside the card body. Now the
  // card is a div with role=link + programmatic navigation. Trade-off:
  // loses middle-click-to-open-in-new-tab (no real <a href>). Worth it
  // for valid HTML + working keyboard a11y.
  const router = useRouter();
  const detailPath = `/machine/${machine.machine_id}`;
  const navigateToDetail = () => router.push(detailPath);

  // Phase 8 — version info from /api/versions for the update-pending badge.
  const { getMachineVersion } = useVersions();
  const versionInfo = getMachineVersion(machine.machine_id);

  // Phase 11 — pin state. PHASE11-NOTE: every card subscribes to the full
  // pinned_machines array, so pinning ONE machine triggers all cards to
  // re-render. Acceptable for v1 (30-50 cards). Future: pass `pinned`
  // boolean from parent, hoisted from a Set.
  const { isPinned, pinMachine, unpinMachine } = usePreferences();
  const pinned = isPinned(machine.machine_id);

  const { status, reason } = classifyMachine(machine);
  const awaiting = isAwaitingData(machine);
  const showPlaceholders = awaiting || status === "offline";

  const ramPct =
    (machine.ram_total_bytes ?? 0) > 0
      ? ((machine.ram_used_bytes ?? 0) / machine.ram_total_bytes) * 100
      : 0;
  const diskPct =
    (machine.disk_total_bytes ?? 0) > 0
      ? ((machine.disk_used_bytes ?? 0) / machine.disk_total_bytes) * 100
      : 0;

  const gpu0 = machine.gpus && machine.gpus.length > 0 ? machine.gpus[0] : null;
  const hasGpu = (machine.gpu_temp ?? 0) > 0 || (gpu0 && gpu0.temp_c > 0);
  const gpuTemp = gpu0 ? gpu0.temp_c : machine.gpu_temp ?? 0;
  const gpuUtil = gpu0 ? gpu0.util_percent : machine.gpu_util_percent ?? 0;
  const gpuVramUsed = gpu0 ? gpu0.mem_used_bytes : machine.gpu_vram_used_bytes ?? 0;
  const gpuVramTotal = gpu0 ? gpu0.mem_total_bytes : machine.gpu_vram_total_bytes ?? 0;

  const tags = machine.tags ? machine.tags.split(",").map((t) => t.trim()).filter(Boolean) : [];
  const adapterTag = tags.find((t) => API_ADAPTERS.includes(t.toLowerCase()));
  const otherTags = tags.filter((t) => !API_ADAPTERS.includes(t.toLowerCase()));
  const isAPIMachine = !!adapterTag;

  const accentClass =
    status === "online"
      ? "before:bg-emerald-500"
      : status === "warning"
      ? "before:bg-amber-500"
      : "before:bg-red-500/60";

  const dotClass =
    status === "online"
      ? "bg-emerald-500"
      : status === "warning"
      ? "bg-amber-500"
      : "bg-red-500/60";

  const cpuTextColorClass =
    (machine.cpu_percent ?? 0) > 90
      ? "text-red-400"
      : (machine.cpu_percent ?? 0) > 70
      ? "text-amber-400"
      : "text-blox-text";

  const gpuTempColorClass =
    gpuTemp > 80
      ? "text-red-400"
      : gpuTemp > 60
      ? "text-amber-400"
      : "text-emerald-400";

  return (
    <div
      role="link"
      tabIndex={0}
      onClick={navigateToDetail}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          navigateToDetail();
        }
      }}
      aria-label={`Open ${machine.hostname} details`}
      className="block h-full cursor-pointer"
    >
      <motion.div
        whileHover={{ y: -2 }}
        transition={{ type: "spring", stiffness: 420, damping: 28 }}
        className={[
          "group relative h-full flex flex-col cursor-pointer",
          "bg-blox-card border border-blox-border rounded-xl",
          "[padding:var(--card-padding)]",
          "before:absolute before:left-0 before:top-3 before:bottom-3 before:w-[3px] before:rounded-full",
          accentClass,
          "hover:border-blox-muted/30 hover:shadow-lg",
          "transition-[border-color,box-shadow] duration-[var(--motion-base)]",
        ].join(" ")}
      >
        {/* row 1: hostname + actions */}
        <div className="flex items-start justify-between gap-2 pl-2.5">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <span
              className={[
                "shrink-0 w-2 h-2 rounded-full",
                dotClass,
                status === "online" ? "animate-status-pulse" : "",
              ].join(" ")}
              aria-label={`Status: ${status}`}
            />
            <span className="font-semibold text-sm text-blox-text truncate">
              {machine.hostname || machine.machine_id}
            </span>
            {reason && (
              <span className="text-[10px] text-amber-400 font-medium shrink-0 tabular-nums">
                {reason}
              </span>
            )}
          </div>

          <div className="flex items-center gap-0.5 shrink-0 -mr-1">
            {/* Phase 11 — pin star, always first in the action group. */}
            <button
              type="button"
              onClick={(e) => {
                e.preventDefault();
                e.stopPropagation();
                if (pinned) {
                  void unpinMachine(machine.machine_id);
                } else {
                  void pinMachine(machine.machine_id);
                }
              }}
              className={`p-1 rounded transition-colors ${
                pinned
                  ? "text-amber-400 hover:text-amber-300"
                  : "text-blox-muted/40 hover:text-amber-400 hover:bg-amber-500/10"
              }`}
              title={pinned ? "Unpin from top" : "Pin to top"}
              aria-label={pinned ? "Unpin machine" : "Pin machine"}
              aria-pressed={pinned}
            >
              <Star className={`w-3 h-3 ${pinned ? "fill-amber-400" : ""}`} />
            </button>
            {onRefresh && status !== "offline" && (
              <RefreshButton
                onRefresh={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onRefresh(machine.machine_id);
                }}
              />
            )}
            {isAPIMachine && onEdit && (
              <button
                type="button"
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onEdit(machine.machine_id);
                }}
                className="p-1 rounded text-blox-muted/40 hover:text-blox-blue hover:bg-blox-blue/10 transition-colors"
                title="Edit API machine"
                aria-label="Edit machine"
              >
                <Pencil className="w-3 h-3" />
              </button>
            )}
            {onDelete && (
              <button
                type="button"
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onDelete(machine.machine_id, machine.hostname);
                }}
                className="p-1 rounded text-blox-muted/40 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                title="Delete machine"
                aria-label="Delete machine"
              >
                <Trash2 className="w-3 h-3" />
              </button>
            )}
          </div>
        </div>

        {/* row 2: ip + adapter / os */}
        <div className="flex items-center gap-1.5 text-[11px] text-blox-muted pl-2.5 mt-0.5 truncate">
          {machine.ip ? (
            <span className="font-mono tabular-nums truncate">{machine.ip}</span>
          ) : (
            <span className="text-blox-muted/50">no ip</span>
          )}
          {adapterTag ? (
            <>
              <span aria-hidden>·</span>
              <span className="uppercase text-[9px] tracking-[0.08em] text-violet-400 font-semibold">
                {adapterTag}
              </span>
            </>
          ) : machine.os ? (
            <>
              <span aria-hidden>·</span>
              <span className="truncate">{machine.os}</span>
            </>
          ) : null}
        </div>

        {/* primary metric */}
        <div className="pl-2.5 mt-3 mb-2">
          {showPlaceholders ? (
            <div className="flex items-baseline gap-2 py-1">
              <span className="text-2xl font-semibold text-blox-muted/30 tabular-nums leading-none">
                —
              </span>
              <span className="text-[11px] text-blox-muted">
                {awaiting
                  ? "Awaiting first metrics…"
                  : `Last seen ${
                      machine.last_seen ? timeSince(machine.last_seen) : "never"
                    }`}
              </span>
            </div>
          ) : (
            <div className="flex items-end gap-3">
              <div className="flex flex-col">
                <div className="flex items-baseline gap-0.5 leading-none">
                  <span className={`text-2xl font-semibold tabular-nums ${cpuTextColorClass}`}>
                    {(machine.cpu_percent ?? 0).toFixed(0)}
                  </span>
                  <span className="text-xs text-blox-muted">%</span>
                </div>
                <span className="text-[9px] text-blox-muted uppercase tracking-[0.1em] mt-1">
                  CPU
                </span>
              </div>
              <div className="flex-1 self-end pb-0.5">
                <Sparkline machineId={machine.machine_id} width={140} height={28} />
              </div>
            </div>
          )}
        </div>

        {/* secondary metrics */}
        <div className="space-y-1.5 pl-2.5 flex-1">
          <CompactMetricRow
            label="RAM"
            pct={ramPct}
            usedBytes={machine.ram_used_bytes}
            totalBytes={machine.ram_total_bytes}
            placeholder={showPlaceholders}
            warningAt={80}
            dangerAt={95}
          />
          <CompactMetricRow
            label="DISK"
            pct={diskPct}
            usedBytes={machine.disk_used_bytes}
            totalBytes={machine.disk_total_bytes}
            placeholder={showPlaceholders}
            warningAt={75}
            dangerAt={90}
          />

          {/* PHASE12-NOTE: chose the *additive* thermal row (CPU + GPU temps
              together) above the existing GPU row instead of pulling GPU
              temp out of the GPU row. The GPU row's "72°C · 85% util · 18/24
              GB" rhythm is load-bearing for visual scan; carving the temp
              out of it would force a layout reflow we'd rather not ship in
              this phase. */}
          {!showPlaceholders && ((machine.cpu_temp_c ?? 0) > 0 || hasGpu) && (
            <div className="flex items-center gap-2 px-0 py-1.5 text-[11px] text-blox-muted pt-1.5 mt-1.5 border-t border-blox-border/30">
              <Thermometer className="w-3 h-3 shrink-0" />
              {(machine.cpu_temp_c ?? 0) > 0 && (
                <>
                  <span className="uppercase tracking-[0.08em] text-[9px] font-medium">CPU</span>
                  <span className={`tabular-nums font-mono ${getTempColorClass(machine.cpu_temp_c!)}`}>
                    {machine.cpu_temp_c!.toFixed(0)}°C
                  </span>
                </>
              )}
              {(machine.cpu_temp_c ?? 0) > 0 && hasGpu && (
                <span className="text-blox-muted/40">·</span>
              )}
              {hasGpu && (
                <>
                  <span className="uppercase tracking-[0.08em] text-[9px] font-medium">GPU</span>
                  <span className={`tabular-nums font-mono ${getTempColorClass(gpuTemp)}`}>
                    {gpuTemp.toFixed(0)}°C
                  </span>
                </>
              )}
            </div>
          )}

          {hasGpu && !showPlaceholders && (
            <div className="flex items-center gap-2 text-[11px] pt-1.5 mt-1.5 border-t border-blox-border/30">
              <Zap className="w-3 h-3 text-blox-muted shrink-0" />
              <span className={`tabular-nums font-mono font-semibold ${gpuTempColorClass}`}>
                {gpuTemp.toFixed(0)}°C
              </span>
              {gpuUtil > 0 && (
                <span className="text-blox-muted tabular-nums">
                  · {gpuUtil.toFixed(0)}% util
                </span>
              )}
              {gpuVramTotal > 0 && (
                <span className="text-blox-muted tabular-nums ml-auto font-mono">
                  {formatBytes(gpuVramUsed)} / {formatBytes(gpuVramTotal)}
                </span>
              )}
            </div>
          )}
        </div>

        {/* footer */}
        <div className="flex items-center gap-1.5 text-[10px] text-blox-muted/70 mt-3 pt-2 border-t border-blox-border/40 pl-2.5 tabular-nums">
          <span className="font-mono">
            <LiveTimeSince since={machine.last_seen ?? null} />
          </span>
          {machine.latency_ms !== undefined && machine.latency_ms > 0 && (
            <>
              <span aria-hidden>·</span>
              <span className="font-mono">{machine.latency_ms}ms</span>
            </>
          )}
          {/* Phase 8 — version badge, only when an update is pending */}
          {versionInfo && versionInfo.update_pending && (
            <>
              <span aria-hidden>·</span>
              <span
                className="inline-flex items-center gap-0.5 px-1 py-0 rounded bg-amber-500/10 text-amber-400 font-mono text-[9px]"
                title={`Running ${versionInfo.running_sha?.slice(0, 8) ?? "unknown"}, update pending`}
              >
                update pending
              </span>
            </>
          )}
          {otherTags.length > 0 && (
            <>
              <span aria-hidden>·</span>
              <span className="truncate">{otherTags.join(", ")}</span>
            </>
          )}
        </div>
      </motion.div>
    </div>
  );
}

/* ============================================================================
 * Compact metric row (RAM / Disk)
 * ============================================================================ */

interface CompactMetricRowProps {
  label: string;
  pct: number;
  usedBytes?: number | null;
  totalBytes?: number | null;
  placeholder: boolean;
  warningAt: number;
  dangerAt: number;
}

function CompactMetricRow({
  label,
  pct,
  usedBytes,
  totalBytes,
  placeholder,
  warningAt,
  dangerAt,
}: CompactMetricRowProps) {
  const variant: ProgressVariant = placeholder
    ? "neutral"
    : (totalBytes ?? 0) === 0
    ? "neutral"
    : deriveMetricVariant(pct, warningAt, dangerAt);

  const valueColorClass =
    variant === "danger"
      ? "text-red-400"
      : variant === "warning"
      ? "text-amber-400"
      : "text-blox-text";

  return (
    <div className="grid grid-cols-[2.25rem_1fr_auto] items-center gap-2 text-[11px]">
      <span className="text-blox-muted uppercase tracking-[0.08em] text-[9px] font-medium">
        {label}
      </span>
      <ProgressBar value={placeholder ? 0 : pct} variant={variant} />
      <div className="flex items-center gap-1.5 tabular-nums shrink-0 min-w-0">
        {placeholder ? (
          <span className="text-blox-muted/40 font-mono">—</span>
        ) : (
          <>
            <span className={`font-mono font-medium ${valueColorClass}`}>
              {pct.toFixed(0)}%
            </span>
            {(totalBytes ?? 0) > 0 && (
              <span className="text-blox-muted/70 font-mono text-[10px]">
                {formatBytes(usedBytes)}/{formatBytes(totalBytes)}
              </span>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/* ============================================================================
 * LiveTimeSince — 1Hz ticking relative timestamp
 *
 * Each card mounts its own 1Hz interval so the footer "12s ago" advances
 * every second without waiting for a fresh SSE event to re-render the
 * parent. Lightweight: a single setInterval per card, no parent re-renders.
 * ============================================================================ */

function LiveTimeSince({ since, prefix = "" }: { since: number | null; prefix?: string }) {
  const [, force] = useState(0);
  useEffect(() => {
    if (!since) return;
    const id = setInterval(() => force((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [since]);
  if (!since) return <span className="text-blox-muted/40">never</span>;
  return <span>{prefix}{timeSince(since)}</span>;
}

/* ============================================================================
 * RefreshButton — per-card refresh icon with self-contained spinner state
 * ============================================================================ */

function RefreshButton({ onRefresh }: { onRefresh: (e: React.MouseEvent) => void }) {
  const [refreshing, setRefreshing] = useState(false);

  const handleClick = (e: React.MouseEvent) => {
    if (refreshing) return;
    setRefreshing(true);
    onRefresh(e);
    // Stop the spinner after 1.5s — fresh metrics typically arrive within
    // ~500ms via SSE, but we don't get an explicit "refresh complete"
    // signal so we time out the visual feedback.
    setTimeout(() => setRefreshing(false), 1500);
  };

  return (
    <button
      type="button"
      onClick={handleClick}
      disabled={refreshing}
      className="p-1 rounded text-blox-muted/40 hover:text-blox-blue hover:bg-blox-blue/10 transition-colors disabled:opacity-50"
      title="Refresh metrics"
      aria-label="Refresh metrics for this machine"
    >
      <RefreshCw className={`w-3 h-3 ${refreshing ? "animate-spin" : ""}`} />
    </button>
  );
}

/* ============================================================================
 * Skeleton (initial load)
 * ============================================================================ */

export function MachineCardSkeleton() {
  return (
    <div className="bg-blox-card border border-blox-border rounded-xl p-4 h-full flex flex-col relative before:absolute before:left-0 before:top-3 before:bottom-3 before:w-[3px] before:rounded-full before:bg-blox-border">
      <div className="flex items-center gap-2 pl-2.5">
        <div className="w-2 h-2 rounded-full bg-blox-border" />
        <div className="h-3.5 bg-blox-border rounded w-28 animate-shimmer" />
      </div>
      <div className="pl-2.5 mt-1.5">
        <div className="h-2.5 bg-blox-border/60 rounded w-32 animate-shimmer" />
      </div>
      <div className="flex items-end gap-3 pl-2.5 mt-3 mb-2">
        <div className="flex flex-col gap-1.5">
          <div className="h-7 w-12 bg-blox-border rounded animate-shimmer" />
          <div className="h-2 w-8 bg-blox-border/60 rounded animate-shimmer" />
        </div>
        <div className="flex-1 h-7 bg-blox-border/40 rounded animate-shimmer" />
      </div>
      <div className="space-y-1.5 pl-2.5 flex-1">
        <div className="h-2.5 bg-blox-border/40 rounded animate-shimmer" />
        <div className="h-2.5 bg-blox-border/40 rounded animate-shimmer" />
      </div>
      <div className="h-2 w-20 bg-blox-border/40 rounded mt-3 pl-2.5 animate-shimmer" />
    </div>
  );
}
