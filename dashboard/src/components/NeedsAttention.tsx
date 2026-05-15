"use client";

import { useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useRouter } from "next/navigation";
import { AlertTriangle } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import {
  classifyMachine,
  isProblem,
  STATUS_ORDER,
  STATUS_VIS,
  StatusDot,
  type MachineStatus,
} from "@/components/StatusBadge";

/* ============================================================================
 * NeedsAttention — pinned-to-top stripe showing problem machines.
 *
 * Only renders when there's at least one machine in {critical, warning,
 * offline, stale}. Sorts by status severity (critical first) then alpha
 * by hostname. Horizontally scrollable on narrow viewports; on desktop
 * a flex-wrap grid lets all chips show at once.
 *
 * Each chip is a single-line summary that links to /machine/:id. The
 * point: when something is wrong, the operator sees what and where in
 * <2 seconds without scrolling the main grid.
 * ============================================================================ */

interface NeedsAttentionProps {
  machines: MachineMetrics[];
}

interface ProblemEntry {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
}

function timeAgo(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

export function NeedsAttention({ machines }: NeedsAttentionProps) {
  const router = useRouter();

  const problems = useMemo<ProblemEntry[]>(() => {
    const out: ProblemEntry[] = [];
    for (const m of machines) {
      const { status, reason } = classifyMachine(m);
      if (isProblem(status)) out.push({ machine: m, status, reason });
    }
    out.sort((a, b) => {
      const sd = STATUS_ORDER[a.status] - STATUS_ORDER[b.status];
      if (sd !== 0) return sd;
      return (a.machine.hostname ?? "").localeCompare(b.machine.hostname ?? "");
    });
    return out;
  }, [machines]);

  const counts = useMemo(() => {
    let critical = 0, warning = 0, offline = 0, stale = 0;
    for (const p of problems) {
      if (p.status === "critical") critical++;
      else if (p.status === "warning") warning++;
      else if (p.status === "offline") offline++;
      else if (p.status === "stale") stale++;
    }
    return { critical, warning, offline, stale, total: problems.length };
  }, [problems]);

  return (
    <AnimatePresence>
      {problems.length > 0 && (
        <motion.div
          initial={{ opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: "auto" }}
          exit={{ opacity: 0, height: 0 }}
          transition={{ duration: 0.2 }}
          className="overflow-hidden border-y border-border-subtle bg-surface-sunken/60"
        >
          <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-2.5">
            <div className="flex items-start gap-3">
              <div className="flex items-center gap-2 pt-1.5 shrink-0">
                <AlertTriangle className="h-3 w-3 text-status-critical" />
                <span className="text-[10px] font-medium uppercase tracking-[0.14em] text-text-tertiary">
                  Needs Attention
                </span>
                <span className="metric-figure text-[10px] text-text-primary">
                  {counts.total}
                </span>
                <span className="inline-flex items-center gap-1.5 ml-1">
                  {counts.critical > 0 && <CountChip n={counts.critical} label="crit" color="text-status-critical" />}
                  {counts.warning > 0 && <CountChip n={counts.warning} label="warn" color="text-status-warning" />}
                  {counts.offline > 0 && <CountChip n={counts.offline} label="off" color="text-status-offline" />}
                  {counts.stale > 0 && <CountChip n={counts.stale} label="stale" color="text-status-stale" />}
                </span>
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex flex-wrap gap-1.5">
                  {problems.map(({ machine, status, reason }) => (
                    <ProblemChip
                      key={machine.machine_id}
                      machine={machine}
                      status={status}
                      reason={reason}
                      onOpen={() => router.push(`/machine/${machine.machine_id}`)}
                    />
                  ))}
                </div>
              </div>
            </div>
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

/* ============================================================================
 * ProblemChip — single problem-machine summary.
 * ============================================================================ */

function ProblemChip({
  machine,
  status,
  reason,
  onOpen,
}: {
  machine: MachineMetrics;
  status: MachineStatus;
  reason?: string;
  onOpen: () => void;
}) {
  const vis = STATUS_VIS[status];
  // For offline, use "last seen Xm ago" instead of a metric reason.
  const detail =
    status === "offline"
      ? machine.last_seen
        ? timeAgo(machine.last_seen)
        : "never seen"
      : reason ?? vis.label.toLowerCase();

  return (
    <button
      type="button"
      onClick={onOpen}
      className={[
        "group inline-flex items-center gap-2 rounded-md px-2.5 py-1.5",
        vis.surfaceTintClass || "bg-surface-raised",
        "border border-border-subtle hover:border-border-strong",
        "transition-colors duration-[var(--motion-fast)]",
        "text-left",
      ].join(" ")}
      title={`${machine.hostname} — ${detail}`}
    >
      <StatusDot status={status} size="xs" />
      <span className="font-semibold text-[12px] text-text-primary truncate max-w-[160px]">
        {machine.hostname || machine.machine_id}
      </span>
      <span className={`metric-figure text-[10px] ${vis.textClass}`}>
        {detail}
      </span>
    </button>
  );
}

function CountChip({ n, label, color }: { n: number; label: string; color: string }) {
  return (
    <span className={`inline-flex items-baseline gap-0.5 text-[9px] ${color}`}>
      <span className="metric-figure">{n}</span>
      <span className="uppercase tracking-[0.08em]">{label}</span>
    </span>
  );
}
