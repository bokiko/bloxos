"use client";

// Monoform — the one way a route shows state.
//
// Green, amber, red and violet mean nominal, warning, critical and stale, and
// nothing else; `neutral` is for "not applicable / not reported", which is a
// fact rather than a health reading. Every mark carries a label and either a
// dot or a Lucide icon, so the meaning survives without colour — which is the
// rule, not a nicety.

import type { LucideIcon } from "lucide-react";

export type MonoformTone = "ok" | "warning" | "critical" | "stale" | "neutral";

const TONE_CLASS: Record<MonoformTone, string> = {
  ok: "mf-status-live",
  warning: "mf-status-warning",
  critical: "mf-status-critical",
  stale: "mf-status-stale",
  neutral: "text-text-tertiary",
};

interface StatusMarkProps {
  tone: MonoformTone;
  /** The words that carry the state. Never omit them. */
  label: string;
  /** Optional icon; a plain dot is used when none is given. */
  Icon?: LucideIcon;
  /** Quieter trailing detail on the same line. */
  detail?: string;
  className?: string;
  title?: string;
}

export function StatusMark({ tone, label, Icon, detail, className, title }: StatusMarkProps) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-[13px] ${TONE_CLASS[tone]} ${className ?? ""}`}
      title={title}
    >
      {Icon ? (
        <Icon className="w-3.5 h-3.5 shrink-0" aria-hidden />
      ) : (
        <span className="mf-status-dot" aria-hidden />
      )}
      <span>{label}</span>
      {detail && <span className="text-text-tertiary">{detail}</span>}
    </span>
  );
}

/**
 * The same mark at table scale — 11px, mono, for dense rows where the value
 * columns are already mono.
 */
export function StatusCell({ tone, label, Icon, title }: Omit<StatusMarkProps, "className" | "detail">) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 text-[11px] font-mono ${TONE_CLASS[tone]}`}
      title={title}
    >
      {Icon ? (
        <Icon className="w-3 h-3 shrink-0" aria-hidden />
      ) : (
        <span className="mf-status-dot" aria-hidden />
      )}
      <span>{label}</span>
    </span>
  );
}
