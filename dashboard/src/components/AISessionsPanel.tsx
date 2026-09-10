"use client";

// AI Sessions — per-machine panel rendered in the machine detail "AI
// Sessions" tab. Read-only.

import { WifiOff } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";
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
    <section className="mf-panel overflow-hidden" data-testid="ai-sessions-panel">
      <div className={MF_PANEL_HEAD}>
        <div className="flex items-center gap-2.5">
          <h2 className={MF_PANEL_TITLE}>AI Sessions</h2>
          {enabled !== false && machine && (
            <span className="mf-metric text-[11px] text-text-tertiary">
              {count} session{count === 1 ? "" : "s"}
            </span>
          )}
        </div>
        <div className="flex items-center gap-3">
          {machine && <StaleNotice machine={machine} now={now} staleAfterSeconds={staleAfterSeconds} />}
          {!connected && (
            <span
              className="mf-status-warning inline-flex items-center gap-1.5 font-mono text-[11px]"
              role="status"
            >
              <WifiOff className="w-3 h-3" aria-hidden />
              live updates paused
            </span>
          )}
        </div>
      </div>
      <div className="px-4 py-4">{body}</div>
      {enabled !== false && count > 0 && (
        <div className="border-t border-border-subtle px-6 py-3.5">
          <SessionsLegend />
        </div>
      )}
    </section>
  );
}
