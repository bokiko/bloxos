"use client";

// AI Sessions — admin switch. Rendered only for fleet.admin (the settings
// page gates the tab); the hub enforces the same scope on PATCH.

import { useState } from "react";
import { Bot, ShieldCheck } from "lucide-react";
import { useAISessions } from "@/contexts/AISessionsContext";
import { useToast } from "@/components/Toast";
import { cn } from "@/lib/utils";

export function AISessionsSettings() {
  const { enabled, hasLoaded, setEnabled } = useAISessions();
  const { addToast } = useToast();
  const [saving, setSaving] = useState(false);
  const isOn = enabled === true;

  const toggle = async () => {
    if (saving || enabled === null) return;
    setSaving(true);
    try {
      await setEnabled(!isOn);
      addToast("success", !isOn ? "AI Sessions monitoring enabled" : "AI Sessions monitoring disabled");
    } catch (e) {
      addToast("error", e instanceof Error ? e.message : "Failed to update AI Sessions");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-8">
      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <div className="flex items-start justify-between gap-6 flex-wrap">
          <div className="min-w-0 max-w-2xl">
            <h2 className="text-sm font-semibold text-blox-text mb-1 inline-flex items-center gap-2">
              <Bot className="w-4 h-4 text-blox-blue" aria-hidden />
              AI Sessions monitoring
            </h2>
            <p className="text-xs text-blox-muted">
              Show which AI coding tools (Claude Code, Codex, Kimi) are running on fleet machines.
            </p>
            <ul className="mt-3 space-y-1.5 text-xs text-blox-muted">
              <li className="flex gap-2">
                <ShieldCheck className="w-3.5 h-3.5 text-emerald-400 mt-px shrink-0" aria-hidden />
                <span>
                  <span className="text-blox-text">Metadata only.</span> The tool, an explicitly chosen model,
                  the project folder name, start time and a coarse CPU-based activity hint. Never prompts,
                  responses, transcripts, commands, environment variables, full paths or usernames.
                </span>
              </li>
              <li className="flex gap-2">
                <ShieldCheck className="w-3.5 h-3.5 text-emerald-400 mt-px shrink-0" aria-hidden />
                <span>
                  <span className="text-blox-text">Live only.</span> Sessions disappear when the process
                  ends or the machine disconnects. Nothing is stored.
                </span>
              </li>
            </ul>
          </div>

          <button
            type="button"
            role="switch"
            aria-checked={isOn}
            aria-label="AI Sessions monitoring"
            disabled={!hasLoaded || saving}
            onClick={() => void toggle()}
            className={cn(
              "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blox-blue/50",
              "disabled:opacity-50 disabled:cursor-not-allowed",
              isOn ? "bg-blox-blue border-blox-blue" : "bg-blox-bg border-blox-border",
            )}
          >
            <span
              className={cn(
                "inline-block h-4.5 w-4.5 rounded-full bg-white shadow transition-transform",
                isOn ? "translate-x-[22px]" : "translate-x-[2px]",
              )}
              aria-hidden
            />
          </button>
        </div>

        <div className="mt-4 flex items-center gap-2 text-xs">
          <span className="text-blox-muted">Status:</span>
          <span className={cn("font-medium", isOn ? "text-emerald-400" : "text-blox-muted")}>
            {!hasLoaded ? "loading…" : isOn ? "On — agents report live sessions" : "Off — agents do not scan"}
          </span>
        </div>

        <p className="mt-4 text-[11px] text-blox-muted">
          Turning this off is pushed to every connected agent, which stops scanning immediately. A machine
          can also opt out on its own by setting <code className="font-mono text-blox-text">BLOXOS_AI_SESSIONS=0</code> in
          the agent&apos;s environment; that local opt-out cannot be overridden from here.
        </p>
      </section>
    </div>
  );
}
