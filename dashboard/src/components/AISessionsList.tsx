"use client";

// AI Sessions — shared presentation for one machine's session list plus the
// empty / disabled / loading / stale states. Used by the machine detail tab
// and the fleet-wide /sessions page so both read the contract identically.
//
// Read-only by design: there is nothing here that acts on a session.
//
// Monoform notes:
//   - Tool identity and confidence are *attributes*, not health readings, so
//     they are drawn in neutral chips. Green/amber/red stay reserved for real
//     nominal/warning/critical state — which here means "the process is
//     running" and "the snapshot is stale", and nothing else.
//   - The running marker used to be a pulsing dot. Monoform has no continuous
//     animation, so it is a plain status dot plus the word.

import Link from "next/link";
import { Bot, Clock, FolderGit2, Info, PowerOff } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { StatusCell } from "@/components/MonoformStatus";
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
  toolLabel,
} from "@/lib/ai-sessions";
import { cn } from "@/lib/utils";

/** Neutral chip shared by the tool name and the confidence mark. */
const CHIP =
  "inline-flex items-center rounded-md border border-border-default px-1.5 py-px text-[10px] " +
  "font-mono leading-[1.5] whitespace-nowrap";

/* ---------------------------------------------------------------------------
 * Attribute rendering — value + explicit confidence. Never colour alone.
 * ------------------------------------------------------------------------- */

function ConfidenceMark({ confidence }: { confidence: string }) {
  const label = confidenceLabel(confidence);
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
              CHIP,
              "uppercase tracking-[0.05em]",
              confidence === "exact" ? "text-text-secondary" : "text-text-tertiary",
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
      <span className="inline-flex items-center gap-1.5 text-text-tertiary">
        <span className="text-xs">model unknown</span>
        <ConfidenceMark confidence="unknown" />
      </span>
    );
  }
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <span className="truncate font-mono text-xs text-text-primary" title={model.value}>
        {model.value}
      </span>
      <ConfidenceMark confidence={model.confidence} />
    </span>
  );
}

export function ProjectCell({ project }: { project: AIAttr }) {
  if (!project.value) {
    return <span className="text-xs text-text-tertiary">no project</span>;
  }
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5" title="Working directory name">
      <FolderGit2 className="w-3 h-3 shrink-0 text-text-tertiary" aria-hidden />
      <span className="truncate font-mono text-xs text-text-primary">{project.value}</span>
    </span>
  );
}

export function ActivityCell({ activity }: { activity: AIAttr }) {
  const label = activityLabel(activity);
  const known = activity.confidence !== "unknown" && !!activity.value;
  const busy = known && activity.value === "active";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs",
        busy ? "mf-status-live" : known ? "text-text-secondary" : "text-text-tertiary",
      )}
    >
      <span
        className={cn("mf-status-dot", !busy && "bg-current opacity-45")}
        aria-hidden
      />
      <span className={busy ? "text-text-primary" : undefined}>{label}</span>
    </span>
  );
}

/** The one definitive state: a session exists because its process does. */
export function RunningBadge() {
  return <StatusCell tone="ok" label="Running" />;
}

export function ToolChip({ tool }: { tool: string }) {
  return <span className={cn(CHIP, "text-text-secondary")}>{toolLabel(tool)}</span>;
}

/* ---------------------------------------------------------------------------
 * One session row.
 * ------------------------------------------------------------------------- */

export function SessionRow({ session, now }: { session: AISession; now: number }) {
  const running = formatRunningFor(session.started_at, now);
  return (
    <li
      className="flex flex-col gap-1.5 rounded-lg px-3 py-2.5 transition-colors hover:bg-surface-elevated sm:grid sm:grid-cols-[6.5rem_minmax(0,1.4fr)_minmax(0,1fr)_5.5rem_minmax(0,1fr)] sm:items-center sm:gap-x-3 sm:gap-y-0"
      data-session-id={session.id}
    >
      {/* On phones this is one line (chip + model); on wider screens the
          two children become their own grid cells. */}
      <div className="flex min-w-0 items-center gap-2 sm:contents">
        <ToolChip tool={session.tool} />
        <div className="min-w-0">
          <ModelCell model={session.model} />
        </div>
      </div>
      <div className="min-w-0">
        <ProjectCell project={session.project} />
      </div>
      <div
        className="mf-metric inline-flex items-center gap-1.5 text-xs text-text-tertiary"
        title="Running for"
      >
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
    <span role="status">
      <StatusCell
        tone="stale"
        label={`stale · last report ${age >= 120 ? `${Math.floor(age / 60)}m` : `${age}s`} ago`}
      />
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
        <div key={i} className="h-9 rounded-lg bg-border-default/30 animate-shimmer" />
      ))}
    </div>
  );
}

export function SessionsDisabledNotice({ canManage }: { canManage: boolean }) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-text-tertiary">
      <PowerOff className="mb-2 h-7 w-7 opacity-30" aria-hidden />
      <p className="text-[13px] text-text-primary">AI Sessions monitoring is turned off</p>
      <p className="mt-1 max-w-sm text-xs leading-relaxed">
        An administrator disabled it for this hub. Agents stop scanning and nothing is reported.
      </p>
      {canManage && (
        <Link href="/settings?tab=ai-sessions" className="mt-3 text-xs text-accent hover:underline">
          Manage in Settings
        </Link>
      )}
    </div>
  );
}

export function SessionsEmpty({ scope }: { scope: "machine" | "fleet" }) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-text-tertiary">
      <Bot className="mb-2 h-7 w-7 opacity-30" aria-hidden />
      <p className="text-[13px] text-text-primary">No AI coding sessions running</p>
      <p className="mt-1 max-w-sm text-xs leading-relaxed">
        {scope === "fleet"
          ? "Claude Code, Codex and Kimi sessions appear here while they run on any connected machine."
          : "Claude Code, Codex and Kimi sessions appear here while they run on this machine."}
      </p>
    </div>
  );
}

export function SessionsNotReporting() {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center text-text-tertiary">
      <Info className="mb-2 h-7 w-7 opacity-30" aria-hidden />
      <p className="text-[13px] text-text-primary">No session report from this machine</p>
      <p className="mt-1 max-w-sm text-xs leading-relaxed">
        Reporting needs a connected agent that supports AI Sessions. Offline machines and older agents
        show nothing here.
      </p>
    </div>
  );
}

export function SessionsErrorNotice({ error, onRetry }: { error: string; onRetry?: () => void }) {
  return (
    <div
      className="mf-panel flex items-start gap-3 border-status-critical/40 px-5 py-4"
      role="alert"
    >
      <Info className="mt-0.5 h-4 w-4 shrink-0 text-status-critical" aria-hidden />
      <div className="flex-1 text-xs">
        <p className="text-[13px] text-text-primary">Could not load AI sessions</p>
        <p className="mt-0.5 text-text-tertiary">{error}</p>
      </div>
      {onRetry && (
        <button type="button" onClick={onRetry} className="text-xs text-accent hover:underline">
          Retry
        </button>
      )}
    </div>
  );
}

/** Small legend explaining the markers; shown once per surface. */
export function SessionsLegend() {
  return (
    <p className="text-[11px] leading-[1.7] text-text-tertiary">
      <span className="text-text-primary">Running</span> means the tool&apos;s process exists on the
      machine — the only state here that is measured rather than inferred. Model and activity carry a
      confidence mark: <span className="text-text-secondary">exact</span> was observed directly,{" "}
      <span className="text-text-secondary">inferred</span> is a hint, and{" "}
      <span className="text-text-secondary">unknown</span> has no evidence. Idle is an inferred CPU
      reading, not a sign the session ended.
    </p>
  );
}
