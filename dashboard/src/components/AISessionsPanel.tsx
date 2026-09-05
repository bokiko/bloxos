"use client";

// AI Sessions — per-machine panel rendered in the machine detail "AI
// Sessions" tab. Read-only.

import { Bot } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useAISessions, useNow } from "@/contexts/AISessionsContext";
import { useSSE } from "@/contexts/SSEContext";
import {
  SessionList,
  SessionsDisabledNotice,
  SessionsEmpty,
  SessionsErrorNotice,
  SessionsLegend,
  SessionsNotReporting,
  SessionsSkeleton,
  StaleNotice,
} from "@/components/AISessionsList";

export function AISessionsPanel({ machineId }: { machineId: string }) {
  const { enabled, hasLoaded, error, staleAfterSeconds, getMachine, refresh } = useAISessions();
  const { connected } = useSSE();
  const { hasScope } = useAuth();
  const now = useNow();
  const machine = getMachine(machineId);
  const count = machine?.sessions.length ?? 0;

  let body: React.ReactNode;
  if (!hasLoaded) {
    body = <SessionsSkeleton />;
  } else if (error && !machine) {
    body = <SessionsErrorNotice error={error} onRetry={() => void refresh()} />;
  } else if (enabled === false) {
    body = <SessionsDisabledNotice canManage={hasScope("fleet.admin")} />;
  } else if (!machine) {
    body = <SessionsNotReporting />;
  } else if (count === 0) {
    body = <SessionsEmpty scope="machine" />;
  } else {
    body = <SessionList sessions={machine.sessions} now={now} />;
  }

  return (
    <div className="bg-blox-card border border-blox-border rounded-xl p-5" data-testid="ai-sessions-panel">
      <div className="flex items-center justify-between gap-3 mb-4 flex-wrap">
        <div className="flex items-center gap-2.5">
          <div className="p-1.5 rounded-lg bg-blox-blue/10">
            <Bot className="w-3.5 h-3.5 text-blox-blue" aria-hidden />
          </div>
          <h3 className="text-sm font-semibold text-blox-text">AI Sessions</h3>
          {enabled !== false && machine && (
            <span className="text-[10px] text-blox-muted font-mono tabular-nums">({count})</span>
          )}
        </div>
        <div className="flex items-center gap-3">
          {machine && <StaleNotice machine={machine} now={now} staleAfterSeconds={staleAfterSeconds} />}
          {!connected && (
            <span className="text-[10px] text-status-warning uppercase tracking-[0.06em]" role="status">
              live updates paused
            </span>
          )}
        </div>
      </div>
      {body}
      {enabled !== false && count > 0 && (
        <div className="mt-4 pt-3 border-t border-blox-border/60">
          <SessionsLegend />
        </div>
      )}
    </div>
  );
}
