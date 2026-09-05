"use client";

// AI Sessions — shared presentation for one machine's session list plus the
// empty / disabled / loading / stale states. Used by the machine detail tab
// and the fleet-wide /sessions page so both read the contract identically.
//
// Read-only by design: there is nothing here that acts on a session.

import Link from "next/link";
import { Bot, Clock, FolderGit2, Info, PowerOff, WifiOff } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  type AIAttr,
  type AISession,
  type AISessionsMachine,
  activityLabel,
  confidenceLabel,
  formatRunningFor,
  isSnapshotStale,
  snapshotAgeSeconds,
  sortSessions,
  toolChipClass,
  toolLabel,
} from "@/lib/ai-sessions";
import { cn } from "@/lib/utils";

/* ---------------------------------------------------------------------------
 * Attribute rendering — value + explicit confidence. Never colour alone.
 * ------------------------------------------------------------------------- */

function ConfidenceMark({ confidence }: { confidence: string }) {
  const label = confidenceLabel(confidence);
  const cls =
    confidence === "exact"
      ? "text-emerald-400 border-emerald-500/30"
      : confidence === "inferred"
        ? "text-amber-400 border-amber-500/30"
        : "text-blox-muted border-blox-border";
  const help =
    confidence === "exact"
      ? "Observed verbatim from the process."
      : confidence === "inferred"
        ? "Derived from indirect evidence; treat as a hint."
        : "No evidence available.";
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={cn(
              "inline-flex items-center rounded-sm border px-1 py-px text-[9px] font-medium uppercase tracking-[0.08em] leading-none",
              cls,
            )}
            aria-label={`confidence ${label}`}
          />
        }
      >
        {label}
      </TooltipTrigger>
      <TooltipContent side="top">{help}</TooltipContent>
    </Tooltip>
  );
}

export function ModelCell({ model }: { model: AIAttr }) {
  if (!model.value || model.confidence === "unknown") {
    return (
      <span className="inline-flex items-center gap-1.5 text-blox-muted">
        <span className="text-xs">model unknown</span>
        <ConfidenceMark confidence="unknown" />
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 min-w-0">
      <span className="text-xs font-mono text-blox-text truncate" title={model.value}>
        {model.value}
      </span>
      <ConfidenceMark confidence={model.confidence} />
    </span>
  );
}

export function ProjectCell({ project }: { project: AIAttr }) {
  if (!project.value) {
    return <span className="text-xs text-blox-muted">no project</span>;
  }
  return (
    <span className="inline-flex items-center gap-1.5 min-w-0" title="Working directory name">
      <FolderGit2 className="w-3 h-3 text-blox-muted shrink-0" aria-hidden />
      <span className="text-xs font-mono text-blox-text truncate">{project.value}</span>
    </span>
  );
}

export function ActivityCell({ activity }: { activity: AIAttr }) {
  const label = activityLabel(activity);
  const known = activity.confidence !== "unknown" && !!activity.value;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs",
        known ? "text-blox-text" : "text-blox-muted",
      )}
    >
      <span
        className={cn(
          "inline-block w-1.5 h-1.5 rounded-full shrink-0",
          !known ? "bg-blox-muted/40" : activity.value === "active" ? "bg-blox-blue" : "bg-blox-muted/60",
        )}
        aria-hidden
      />
      <span>{label}</span>
    </span>
  );
}

/** The one definitive state: a session exists because its process does. */
export function RunningBadge() {
  return (
    <Badge
      variant="outline"
      className="border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px] gap-1 h-auto py-0 px-1.5"
    >
      <span className="relative inline-flex h-1.5 w-1.5" aria-hidden>
        <span className="absolute inset-0 rounded-full bg-emerald-400 opacity-60 animate-status-pulse" />
        <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-emerald-400" />
      </span>
      Running
    </Badge>
  );
}

export function ToolChip({ tool }: { tool: string }) {
  return (
    <Badge variant="outline" className={cn("text-[10px] h-auto py-0 px-1.5", toolChipClass(tool))}>
      {toolLabel(tool)}
    </Badge>
  );
}

/* ---------------------------------------------------------------------------
 * One session row.
 * ------------------------------------------------------------------------- */

export function SessionRow({ session, now }: { session: AISession; now: number }) {
  const running = formatRunningFor(session.started_at, now);
  return (
    <li
      className="flex flex-col gap-1.5 sm:grid sm:grid-cols-[6.5rem_minmax(0,1.4fr)_minmax(0,1fr)_5rem_minmax(0,1fr)] sm:items-center sm:gap-x-3 sm:gap-y-0 px-2 py-2 rounded-lg hover:bg-blox-border/20 transition-colors"
      data-session-id={session.id}
    >
      {/* On phones this is one line (chip + model); on wider screens the
          two children become their own grid cells. */}
      <div className="flex items-center gap-2 min-w-0 sm:contents">
        <ToolChip tool={session.tool} />
        <div className="min-w-0">
          <ModelCell model={session.model} />
        </div>
      </div>
      <div className="min-w-0">
        <ProjectCell project={session.project} />
      </div>
      <div className="inline-flex items-center gap-1 text-xs text-blox-muted tabular-nums" title="Running for">
        <Clock className="w-3 h-3 shrink-0" aria-hidden />
        {session.started_at ? (
          <time dateTime={session.started_at}>{running}</time>
        ) : (
          <span>{running}</span>
        )}
      </div>
      <div className="flex items-center justify-between gap-2">
        <ActivityCell activity={session.activity} />
        <RunningBadge />
      </div>
    </li>
  );
}

/* ---------------------------------------------------------------------------
 * A machine's session group (used on /sessions) and its stale marker.
 * ------------------------------------------------------------------------- */

export function StaleNotice({ machine, now, staleAfterSeconds }: { machine: AISessionsMachine; now: number; staleAfterSeconds: number }) {
  if (!isSnapshotStale(machine.receivedAtLocal, now, staleAfterSeconds)) return null;
  const age = snapshotAgeSeconds(machine.receivedAtLocal, now);
  return (
    <span
      className="inline-flex items-center gap-1 text-[10px] text-status-stale uppercase tracking-[0.06em]"
      role="status"
    >
      <WifiOff className="w-3 h-3" aria-hidden />
      stale · last report {age >= 120 ? `${Math.floor(age / 60)}m` : `${age}s`} ago
    </span>
  );
}

export function SessionList({ sessions, now }: { sessions: AISession[]; now: number }) {
  const sorted = sortSessions(sessions);
  return (
    <ul className="space-y-0.5" aria-label="AI coding sessions">
      {sorted.map((s) => (
        <SessionRow key={s.id} session={s} now={now} />
      ))}
    </ul>
  );
}

/* ---------------------------------------------------------------------------
 * States.
 * ------------------------------------------------------------------------- */

export function SessionsSkeleton({ rows = 3 }: { rows?: number }) {
  return (
    <div className="space-y-1.5" aria-busy="true" aria-label="Loading AI sessions">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="h-9 rounded-lg bg-blox-border/30 animate-shimmer" />
      ))}
    </div>
  );
}

export function SessionsDisabledNotice({ canManage }: { canManage: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-blox-muted">
      <PowerOff className="w-8 h-8 mb-2 opacity-30" aria-hidden />
      <p className="text-sm text-blox-text">AI Sessions monitoring is turned off</p>
      <p className="text-xs mt-1 max-w-sm">
        An administrator disabled it for this hub. Agents stop scanning and nothing is reported.
      </p>
      {canManage && (
        <Link href="/settings?tab=ai-sessions" className="text-xs text-blox-blue hover:underline mt-3">
          Manage in Settings
        </Link>
      )}
    </div>
  );
}

export function SessionsEmpty({ scope }: { scope: "machine" | "fleet" }) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-blox-muted">
      <Bot className="w-8 h-8 mb-2 opacity-20" aria-hidden />
      <p className="text-sm text-blox-text">No AI coding sessions running</p>
      <p className="text-xs mt-1 max-w-sm">
        {scope === "fleet"
          ? "Claude Code, Codex and Kimi sessions appear here while they run on any connected machine."
          : "Claude Code, Codex and Kimi sessions appear here while they run on this machine."}
      </p>
    </div>
  );
}

export function SessionsNotReporting() {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-blox-muted">
      <Info className="w-8 h-8 mb-2 opacity-20" aria-hidden />
      <p className="text-sm text-blox-text">No session report from this machine</p>
      <p className="text-xs mt-1 max-w-sm">
        Reporting needs a connected agent that supports AI Sessions. Offline machines and older agents show nothing here.
      </p>
    </div>
  );
}

export function SessionsErrorNotice({ error, onRetry }: { error: string; onRetry?: () => void }) {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2.5" role="alert">
      <Info className="w-4 h-4 text-red-400 mt-0.5 shrink-0" aria-hidden />
      <div className="flex-1 text-xs">
        <p className="text-blox-text">Could not load AI sessions</p>
        <p className="text-blox-muted mt-0.5">{error}</p>
      </div>
      {onRetry && (
        <button type="button" onClick={onRetry} className="text-xs text-blox-blue hover:underline">
          Retry
        </button>
      )}
    </div>
  );
}

/** Small legend explaining the markers; shown once per surface. */
export function SessionsLegend() {
  return (
    <p className="text-[11px] text-blox-muted">
      <span className="text-blox-text">Running</span> means the tool&apos;s process exists on the machine.
      Model and activity carry a confidence mark: <span className="text-emerald-400">exact</span> was observed
      directly, <span className="text-amber-400">inferred</span> is a hint, <span>unknown</span> has no evidence.
      Idle is an inferred CPU reading, not a sign the session ended.
    </p>
  );
}
