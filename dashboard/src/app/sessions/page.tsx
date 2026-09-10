"use client";
import { AppShell } from "@/components/shell/AppShell";

// AI Sessions — fleet-wide live view. Read-only.
//
// Groups the live snapshot by machine. Truth comes from the AISessions
// context (GET on connect, SSE deltas after); this page adds no fetching.
//
// Monoform: the shell owns the title, the rail and every global action, so
// this file starts at the page lead and renders only panels.

import Link from "next/link";
import { useMemo } from "react";
import { Bot, RefreshCw, WifiOff } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useAISessions, useNow } from "@/contexts/AISessionsContext";
import { useSSE } from "@/contexts/SSEContext";
import {
  SessionList,
  SessionsDisabledNotice,
  SessionsEmpty,
  SessionsErrorNotice,
  SessionsLegend,
  SessionsSkeleton,
  StaleNotice,
} from "@/components/AISessionsList";
import { isSnapshotStale, sortMachines, summarize } from "@/lib/ai-sessions";
import { MF_BUTTON } from "@/lib/monoform-classes";
import { cn } from "@/lib/utils";

export default function SessionsPage() {
  return <AppShell><SessionsContent /></AppShell>;
}

function SessionsContent() {
  const { enabled, hasLoaded, loading, error, staleAfterSeconds, machines, refresh } = useAISessions();
  const { connected } = useSSE();
  const { hasScope } = useAuth();
  const now = useNow();

  const ordered = useMemo(() => sortMachines(Array.from(machines.values())), [machines]);
  const withSessions = useMemo(() => ordered.filter((m) => m.sessions.length > 0), [ordered]);
  const totals = useMemo(() => summarize(ordered), [ordered]);

  let body: React.ReactNode;
  if (!hasLoaded) {
    body = (
      <div className="mf-panel p-6">
        <SessionsSkeleton rows={4} />
      </div>
    );
  } else if (error && machines.size === 0) {
    body = <SessionsErrorNotice error={error} onRetry={() => void refresh()} />;
  } else if (enabled === false) {
    body = (
      <div className="mf-panel p-6">
        <SessionsDisabledNotice canManage={hasScope("fleet.admin")} />
      </div>
    );
  } else if (withSessions.length === 0) {
    body = (
      <div className="mf-panel p-6">
        <SessionsEmpty scope="fleet" />
        {totals.reporting > 0 && (
          <p className="text-center text-[11px] text-text-tertiary -mt-4 pb-4">
            {totals.reporting} machine{totals.reporting === 1 ? "" : "s"} reporting, none with a session.
          </p>
        )}
      </div>
    );
  } else {
    body = (
      <div className="space-y-4">
        {withSessions.map((m) => {
          const stale = isSnapshotStale(m.receivedAtLocal, now, staleAfterSeconds);
          return (
            <section
              key={m.machineId}
              aria-labelledby={`ai-sessions-${m.machineId}`}
              className={cn("mf-panel", stale && "opacity-80")}
              data-testid="ai-sessions-machine"
            >
              <div className="flex items-center justify-between gap-3 flex-wrap border-b border-border-subtle px-6 py-3.5">
                <div className="flex items-center gap-2.5 min-w-0">
                  <h2 id={`ai-sessions-${m.machineId}`} className="text-[13px] font-semibold text-text-primary truncate">
                    <Link
                      href={`/machine/${encodeURIComponent(m.machineId)}?tab=ai-sessions`}
                      className="text-accent hover:underline"
                    >
                      {m.hostname || m.machineId}
                    </Link>
                  </h2>
                  <span className="mf-metric text-[11px] text-text-tertiary">
                    {m.sessions.length} session{m.sessions.length === 1 ? "" : "s"}
                  </span>
                </div>
                <StaleNotice machine={m} now={now} staleAfterSeconds={staleAfterSeconds} />
              </div>
              <div className="px-4 py-3">
                <SessionList sessions={m.sessions} now={now} />
              </div>
            </section>
          );
        })}
        <SessionsLegend />
      </div>
    );
  }

  return (
    <>
      <div className="mf-intro">
        <div className="min-w-0">
          <dl className="flex flex-wrap items-baseline gap-x-7 gap-y-2">
            <Stat label="Sessions" value={hasLoaded && enabled !== false ? totals.sessions : "—"} />
            <Stat label="Machines" value={hasLoaded && enabled !== false ? totals.machines : "—"} />
            <div className="flex items-baseline gap-2">
              <dt className="mf-kicker">Feed</dt>
              <dd>
                {enabled === false ? (
                  <span className="inline-flex items-center gap-1.5 text-[13px] text-text-tertiary">
                    <Bot className="w-3.5 h-3.5" aria-hidden />
                    Monitoring off
                  </span>
                ) : connected ? (
                  <span className="mf-status-live inline-flex items-center gap-1.5 text-[13px]">
                    <span className="mf-status-dot" aria-hidden />
                    Live
                  </span>
                ) : (
                  <span className="mf-status-warning inline-flex items-center gap-1.5 text-[13px]" role="status">
                    <WifiOff className="w-3.5 h-3.5" aria-hidden />
                    Updates paused
                  </span>
                )}
              </dd>
            </div>
          </dl>
          <div className="mt-5">
            <button
              type="button"
              onClick={() => void refresh()}
              disabled={loading}
              className={MF_BUTTON}
            >
              <RefreshCw className={cn("w-3.5 h-3.5", loading && "animate-spin")} aria-hidden />
              {loading ? "Refreshing…" : "Refresh sessions"}
            </button>
          </div>
        </div>
        <p>
          Claude Code, Codex and Kimi sessions running across the fleet. Metadata only — the tool, an
          explicitly chosen model, the project folder and how long the process has run. Nothing is kept
          once a session ends.
        </p>
      </div>

      {body}
    </>
  );
}

function Stat({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="mf-kicker">{label}</dt>
      <dd className="mf-metric text-[15px] text-text-primary">{value}</dd>
    </div>
  );
}
