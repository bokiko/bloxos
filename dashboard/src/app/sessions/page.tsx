"use client";

// AI Sessions — fleet-wide live view. Read-only.
//
// Groups the live snapshot by machine. Truth comes from the AISessions
// context (GET on connect, SSE deltas after); this page adds no fetching.

import Link from "next/link";
import { useMemo } from "react";
import { motion } from "framer-motion";
import { ArrowLeft, Bot, RefreshCw, WifiOff } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useAISessions, useNow } from "@/contexts/AISessionsContext";
import { useSSE } from "@/contexts/SSEContext";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { cn } from "@/lib/utils";

export default function SessionsPage() {
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
      <div className="bg-blox-card border border-blox-border rounded-xl p-5">
        <SessionsSkeleton rows={4} />
      </div>
    );
  } else if (error && machines.size === 0) {
    body = <SessionsErrorNotice error={error} onRetry={() => void refresh()} />;
  } else if (enabled === false) {
    body = (
      <div className="bg-blox-card border border-blox-border rounded-xl p-5">
        <SessionsDisabledNotice canManage={hasScope("fleet.admin")} />
      </div>
    );
  } else if (withSessions.length === 0) {
    body = (
      <div className="bg-blox-card border border-blox-border rounded-xl p-5">
        <SessionsEmpty scope="fleet" />
        {totals.reporting > 0 && (
          <p className="text-center text-[11px] text-blox-muted -mt-4 pb-4">
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
              className={cn(
                "bg-blox-card border rounded-xl p-5 transition-opacity",
                stale ? "border-status-stale/30 opacity-80" : "border-blox-border",
              )}
              data-testid="ai-sessions-machine"
            >
              <div className="flex items-center justify-between gap-3 mb-3 flex-wrap">
                <div className="flex items-center gap-2 min-w-0">
                  <h2 id={`ai-sessions-${m.machineId}`} className="text-sm font-semibold text-blox-text truncate">
                    <Link href={`/machine/${encodeURIComponent(m.machineId)}?tab=ai-sessions`} className="hover:underline">
                      {m.hostname || m.machineId}
                    </Link>
                  </h2>
                  <span className="text-[10px] text-blox-muted font-mono tabular-nums">
                    ({m.sessions.length})
                  </span>
                </div>
                <StaleNotice machine={m} now={now} staleAfterSeconds={staleAfterSeconds} />
              </div>
              <SessionList sessions={m.sessions} now={now} />
            </section>
          );
        })}
        <div className="px-1">
          <SessionsLegend />
        </div>
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: "easeOut" }}
      className="min-h-screen bg-blox-bg"
    >
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-12 flex items-center justify-between">
          <Link
            href="/"
            className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-xs"
          >
            <ArrowLeft className="w-3.5 h-3.5" aria-hidden />
            <span>Fleet</span>
          </Link>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void refresh()}
            disabled={loading}
            className="text-xs text-blox-muted hover:text-blox-text gap-1.5 h-8"
          >
            <RefreshCw className={cn("w-3.5 h-3.5", loading && "animate-spin")} aria-hidden />
            Refresh
          </Button>
        </div>
      </header>

      <section className="border-b border-blox-border/50 bg-blox-bg/40">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6">
          <div className="flex items-center gap-3 flex-wrap">
            <h1 className="text-xl sm:text-2xl font-semibold text-blox-text tracking-tight inline-flex items-center gap-2">
              <Bot className="w-5 h-5 text-blox-blue" aria-hidden />
              AI Sessions
            </h1>
            {hasLoaded && enabled !== false && (
              <Badge variant="outline" className="border-blox-border text-blox-muted text-[10px] tabular-nums">
                {totals.sessions} session{totals.sessions === 1 ? "" : "s"} · {totals.machines} machine
                {totals.machines === 1 ? "" : "s"}
              </Badge>
            )}
            {hasLoaded && enabled === false && (
              <Badge variant="outline" className="border-blox-border text-blox-muted text-[10px]">
                off
              </Badge>
            )}
            {!connected && (
              <span className="inline-flex items-center gap-1 text-[11px] text-status-warning" role="status">
                <WifiOff className="w-3 h-3" aria-hidden />
                live updates paused
              </span>
            )}
          </div>
          <p className="text-[12px] text-blox-muted mt-1.5 max-w-2xl">
            Claude Code, Codex and Kimi sessions currently running across the fleet. Metadata only: the
            tool, an explicitly chosen model, the project folder name, and how long the process has run.
            Live only — nothing is kept once a session ends.
          </p>
        </div>
      </section>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">{body}</main>
    </motion.div>
  );
}
