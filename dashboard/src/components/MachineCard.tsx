"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { motion } from "framer-motion";
import { Trash2, Pencil, RefreshCw, Star, ArrowUpFromLine } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import { Sparkline } from "./Sparkline";
import { useVersions } from "@/contexts/VersionsContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import {
  classifyMachine,
  StatusDot,
  StatusBadge,
  STATUS_VIS,
} from "@/components/StatusBadge";

/* ============================================================================
 * MachineCard — dense, operator-focused card.
 *
 * Layout (no left-border accent bar — state lives in the dot, the badge,
 * and a subtle surface tint for warning/critical):
 *
 *     ┌─────────────────────────────────────────────────────────────┐
 *     │ ● hostname           [Warn · CPU 84%]      ⭐ ⟳ ✎ ✕        │
 *     │   192.168.1.45 · ubuntu 24.04 · proxmox · tagA, tagB       │
 *     │                                                             │
 *     │   CPU 73%   RAM 64%   DISK 41%   GPU 72°C  ▂▃▆▇█▇▆          │
 *     │                                                             │
 *     │   ↑ 14h · 23 ms · update pending                            │
 *     └─────────────────────────────────────────────────────────────┘
 *
 * State map (single source of truth in StatusBadge.ts via classifyMachine):
 *   live      surface-raised, dot pulses, no badge, full opacity
 *   stale     surface-raised, "Stale" badge, no value tint
 *   warning   surface-raised + status-warning-tint overlay, "Warn" badge
 *   critical  surface-raised + status-critical-tint overlay + 1px ring
 *   offline   surface-sunken, dimmed values, "Offline · last seen X ago"
 *   loading   real layout, "…" figures with shimmer (NEVER an empty card)
 *
 * Action buttons fade in on hover/focus so the resting state stays calm.
 * ============================================================================ */

const OFFLINE_TINT = "bg-surface-sunken";
const STATE_RING = {
  critical: "ring-1 ring-inset ring-status-critical/30",
  warning: "",
  stale: "",
  offline: "",
  live: "",
} as const;

const API_ADAPTERS = ["synology", "proxmox"];

function timeSince(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const d = Math.floor(hr / 24);
  return `${d}d`;
}

type Tone = "neutral" | "ok" | "warning" | "critical";

function toneText(t: Tone): string {
  if (t === "critical") return "text-status-critical";
  if (t === "warning") return "text-status-warning";
  if (t === "ok") return "text-status-ok";
  return "text-text-primary";
}

function pctTone(pct: number, warn: number, crit: number): Tone {
  if (pct <= 0) return "neutral";
  if (pct >= crit) return "critical";
  if (pct >= warn) return "warning";
  return "neutral";
}

function tempTone(c: number): Tone {
  if (c <= 0) return "neutral";
  if (c >= 86) return "critical";
  if (c >= 75) return "warning";
  return "neutral";
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
  const router = useRouter();
  const detailPath = `/machine/${machine.machine_id}`;
  const navigateToDetail = () => router.push(detailPath);

  const { getMachineVersion } = useVersions();
  const versionInfo = getMachineVersion(machine.machine_id);

  const { isPinned, pinMachine, unpinMachine } = usePreferences();
  const pinned = isPinned(machine.machine_id);

  const { status, reason } = classifyMachine(machine);

  // Loading-vs-loaded distinction:
  //   loading = heartbeat present but no metric values yet (just enrolled)
  //   offline = no heartbeat in >120s
  // Both render with reduced density, but with explicit context so the
  // operator never mistakes them for a healthy card with empty bars.
  const hasMetrics =
    (machine.cpu_percent ?? 0) > 0 ||
    (machine.ram_total_bytes ?? 0) > 0 ||
    (machine.disk_total_bytes ?? 0) > 0;

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

  const tags = machine.tags ? machine.tags.split(",").map((t) => t.trim()).filter(Boolean) : [];
  const adapterTag = tags.find((t) => API_ADAPTERS.includes(t.toLowerCase()));
  const otherTags = tags.filter((t) => !API_ADAPTERS.includes(t.toLowerCase()));
  const isAPIMachine = !!adapterTag;

  const stateVis = STATUS_VIS[status];
  const isOffline = status === "offline";
  const surfaceClass =
    isOffline
      ? OFFLINE_TINT
      : stateVis.surfaceTintClass
      ? `bg-surface-raised ${stateVis.surfaceTintClass}`
      : "bg-surface-raised";

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
      className="group block h-full cursor-pointer"
    >
      <motion.div
        whileHover={{ y: -1 }}
        transition={{ type: "spring", stiffness: 480, damping: 32 }}
        className={[
          "relative h-full flex flex-col",
          surfaceClass,
          "border border-border-default rounded-md",
          STATE_RING[status],
          "px-3.5 py-3",
          "hover:border-border-strong",
          "transition-[border-color,background-color] duration-[var(--motion-fast)]",
          isOffline ? "opacity-75" : "",
        ].join(" ")}
      >
        {/* ── row 1: dot · hostname · status badge · actions ── */}
        <div className="flex items-center gap-2">
          <StatusDot status={status} size="sm" pulse={status === "live"} />
          <span className="font-semibold text-[13px] leading-tight text-text-primary truncate">
            {machine.hostname || machine.machine_id}
          </span>
          {status !== "live" && (
            <StatusBadge status={status} reason={reason} size="xs" />
          )}
          {pinned && (
            <Star className="h-3 w-3 shrink-0 fill-status-warning text-status-warning" aria-label="Pinned" />
          )}
          <div className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity duration-[var(--motion-fast)] group-hover:opacity-100 focus-within:opacity-100">
            <IconAction
              title={pinned ? "Unpin from top" : "Pin to top"}
              onClick={(e) => {
                e.preventDefault();
                e.stopPropagation();
                if (pinned) void unpinMachine(machine.machine_id);
                else void pinMachine(machine.machine_id);
              }}
              tone={pinned ? "warn" : "default"}
            >
              <Star className={`h-3 w-3 ${pinned ? "fill-status-warning" : ""}`} />
            </IconAction>
            {onRefresh && !isOffline && (
              <RefreshButton onRefresh={(e) => { e.preventDefault(); e.stopPropagation(); onRefresh(machine.machine_id); }} />
            )}
            {isAPIMachine && onEdit && (
              <IconAction
                title="Edit API machine"
                onClick={(e) => { e.preventDefault(); e.stopPropagation(); onEdit(machine.machine_id); }}
              >
                <Pencil className="h-3 w-3" />
              </IconAction>
            )}
            {onDelete && (
              <IconAction
                title="Delete machine"
                tone="danger"
                onClick={(e) => { e.preventDefault(); e.stopPropagation(); onDelete(machine.machine_id, machine.hostname); }}
              >
                <Trash2 className="h-3 w-3" />
              </IconAction>
            )}
          </div>
        </div>

        {/* ── row 2: meta (ip · os · adapter · tags) ── */}
        <div className="mt-1 flex items-center gap-1.5 text-[11px] text-text-tertiary truncate metric-figure">
          {machine.ip ? (
            <span className="truncate">{machine.ip}</span>
          ) : (
            <span className="text-text-disabled">no ip</span>
          )}
          {machine.os && (
            <>
              <span className="text-border-strong" aria-hidden>·</span>
              <span className="font-sans truncate">{machine.os}</span>
            </>
          )}
          {adapterTag && (
            <>
              <span className="text-border-strong" aria-hidden>·</span>
              <span className="font-sans font-medium uppercase tracking-[0.1em] text-[9px] text-accent">
                {adapterTag}
              </span>
            </>
          )}
          {otherTags.length > 0 && (
            <>
              <span className="text-border-strong" aria-hidden>·</span>
              <span className="font-sans truncate">{otherTags.join(", ")}</span>
            </>
          )}
        </div>

        {/* ── row 3: metric figures + sparkline (or context for non-live) ── */}
        <div className="mt-3 flex-1">
          {hasMetrics && !isOffline ? (
            <div className="flex items-end justify-between gap-3">
              <div className="flex items-baseline gap-3.5">
                <Figure label="CPU" value={(machine.cpu_percent ?? 0).toFixed(0)} unit="%" tone={pctTone(machine.cpu_percent ?? 0, 75, 92)} />
                <Figure label="RAM" value={ramPct > 0 ? ramPct.toFixed(0) : "—"} unit={ramPct > 0 ? "%" : ""} tone={pctTone(ramPct, 82, 95)} />
                <Figure label="DISK" value={diskPct > 0 ? diskPct.toFixed(0) : "—"} unit={diskPct > 0 ? "%" : ""} tone={pctTone(diskPct, 85, 95)} />
                {hasGpu && (
                  <Figure label="GPU" value={gpuTemp.toFixed(0)} unit="°C" tone={tempTone(gpuTemp)} />
                )}
              </div>
              <div className="shrink-0">
                <Sparkline machineId={machine.machine_id} width={92} height={26} />
              </div>
            </div>
          ) : isOffline ? (
            <div className="flex items-baseline gap-2 text-[11px] text-text-tertiary metric-figure">
              <span className="text-text-disabled">last seen</span>
              <span className="text-text-secondary">
                <LiveTimeSince since={machine.last_seen ?? null} suffix=" ago" />
              </span>
            </div>
          ) : (
            <div className="flex items-baseline gap-2 text-[11px] text-text-tertiary metric-figure">
              <span className="inline-flex h-1.5 w-1.5 rounded-full bg-status-stale animate-status-pulse" />
              <span>awaiting first metrics</span>
            </div>
          )}
        </div>

        {/* ── row 4: footer (uptime · latency · update-pending) ── */}
        <div className="mt-3 flex items-center gap-1.5 text-[10px] text-text-tertiary metric-figure">
          <ArrowUpFromLine className="h-2.5 w-2.5 text-text-disabled shrink-0" />
          <span className={isOffline ? "text-text-disabled" : ""}>
            <LiveTimeSince since={machine.last_seen ?? null} />
          </span>
          {!isOffline && machine.latency_ms !== undefined && machine.latency_ms > 0 && (
            <>
              <span className="text-border-strong" aria-hidden>·</span>
              <span>{machine.latency_ms} ms</span>
            </>
          )}
          {versionInfo && versionInfo.update_pending && (
            <>
              <span className="text-border-strong" aria-hidden>·</span>
              <span
                className="inline-flex items-center gap-1 rounded-sm px-1 py-0 bg-status-warning-tint text-status-warning font-medium"
                title={`Running ${versionInfo.running_sha?.slice(0, 8) ?? "unknown"}, update pending`}
              >
                update pending
              </span>
            </>
          )}
        </div>
      </motion.div>
    </div>
  );
}

/* ============================================================================
 * Figure — the atom of a metric readout. Label small + uppercase, value in
 * Geist Mono with tabular-nums. Tone-colored value when over threshold.
 * ============================================================================ */

function Figure({
  label,
  value,
  unit,
  tone,
}: {
  label: string;
  value: string;
  unit: string;
  tone: Tone;
}) {
  return (
    <div className="flex flex-col gap-0.5 leading-none">
      <span className="text-[9px] font-medium uppercase tracking-[0.12em] text-text-tertiary">{label}</span>
      <span className="inline-flex items-baseline gap-0.5">
        <span className={`metric-value text-base ${toneText(tone)}`}>{value}</span>
        {unit && <span className="metric-unit text-[10px]">{unit}</span>}
      </span>
    </div>
  );
}

/* ============================================================================
 * IconAction — uniform 22px button slot.
 * ============================================================================ */

function IconAction({
  children,
  title,
  onClick,
  tone = "default",
}: {
  children: React.ReactNode;
  title: string;
  onClick: (e: React.MouseEvent) => void;
  tone?: "default" | "danger" | "warn";
}) {
  const toneClass =
    tone === "danger"
      ? "text-text-disabled hover:text-status-critical hover:bg-status-critical-tint"
      : tone === "warn"
      ? "text-status-warning hover:bg-status-warning-tint"
      : "text-text-disabled hover:text-text-primary hover:bg-surface-elevated";
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex h-5 w-5 items-center justify-center rounded-sm transition-colors duration-[var(--motion-fast)] ${toneClass}`}
      title={title}
      aria-label={title}
    >
      {children}
    </button>
  );
}

/* ============================================================================
 * LiveTimeSince — 1Hz ticking relative timestamp.
 * ============================================================================ */

function LiveTimeSince({ since, suffix = "" }: { since: number | null; suffix?: string }) {
  const [, force] = useState(0);
  useEffect(() => {
    if (!since) return;
    const id = setInterval(() => force((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, [since]);
  if (!since) return <span className="text-text-disabled">never</span>;
  return <span>{timeSince(since)}{suffix}</span>;
}

/* ============================================================================
 * RefreshButton — per-card refresh icon with self-contained spinner state.
 * ============================================================================ */

function RefreshButton({ onRefresh }: { onRefresh: (e: React.MouseEvent) => void }) {
  const [refreshing, setRefreshing] = useState(false);
  const handleClick = (e: React.MouseEvent) => {
    if (refreshing) return;
    setRefreshing(true);
    onRefresh(e);
    setTimeout(() => setRefreshing(false), 1500);
  };
  return (
    <IconAction onClick={handleClick} title="Refresh metrics">
      <RefreshCw className={`h-3 w-3 ${refreshing ? "animate-spin" : ""}`} />
    </IconAction>
  );
}

/* ============================================================================
 * MachineCardSkeleton — loading state, visually distinct from any loaded
 * card so the operator never confuses "loading" with "machine has no data".
 *
 * Uses a single muted band with shimmer where the metrics would land —
 * it does NOT mimic the row of figures.
 * ============================================================================ */

export function MachineCardSkeleton() {
  return (
    <div className="bg-surface-raised border border-border-default rounded-md px-3.5 py-3 h-full flex flex-col">
      <div className="flex items-center gap-2">
        <span className="h-2 w-2 rounded-full bg-border-strong animate-status-pulse" />
        <span className="h-3 w-32 rounded-sm bg-border-subtle animate-shimmer" />
      </div>
      <span className="mt-2 h-2 w-44 rounded-sm bg-border-subtle/70 animate-shimmer" />
      <div className="mt-4 flex items-center gap-2 text-[11px] text-text-tertiary">
        <span className="inline-flex h-1.5 w-1.5 rounded-full bg-status-stale animate-status-pulse" />
        <span className="metric-figure">connecting…</span>
      </div>
      <div className="mt-auto pt-3 h-2 w-20 rounded-sm bg-border-subtle/60 animate-shimmer" />
    </div>
  );
}
