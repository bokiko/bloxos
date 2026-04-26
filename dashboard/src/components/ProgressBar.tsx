"use client";

export type ProgressVariant = "neutral" | "active" | "warning" | "danger";

interface ProgressBarProps {
  /** 0–100 percentage. Values outside this range are clamped. */
  value: number;
  /**
   * Explicit color variant. When omitted, derived from value:
   *   value === 0  → neutral (gray, no color)
   *   value < 70   → active  (subtle blue)
   *   value < 90   → warning (amber)
   *   value >= 90  → danger  (red)
   */
  variant?: ProgressVariant;
  size?: "sm" | "md";
  label?: string;
}

function deriveVariant(value: number): ProgressVariant {
  if (value <= 0) return "neutral";
  if (value < 70) return "active";
  if (value < 90) return "warning";
  return "danger";
}

function getBarClass(variant: ProgressVariant): string {
  switch (variant) {
    case "neutral":
      return "bg-blox-border";
    case "warning":
      return "bg-gradient-to-r from-amber-500 to-amber-400";
    case "danger":
      return "bg-gradient-to-r from-red-500 to-red-400";
    case "active":
    default:
      return "bg-gradient-to-r from-blox-blue to-blox-blue/70";
  }
}

function getTrackClass(variant: ProgressVariant): string {
  return variant === "neutral" ? "bg-blox-border/20" : "bg-blox-border/40";
}

export function ProgressBar({ value, variant, size = "sm", label }: ProgressBarProps) {
  const clamped = Math.min(100, Math.max(0, value));
  const v = variant ?? deriveVariant(clamped);
  const height = size === "sm" ? "h-1.5" : "h-2";

  return (
    <div className="flex items-center gap-3 w-full">
      <div className={`flex-1 ${height} rounded-full ${getTrackClass(v)} overflow-hidden`}>
        <div
          className={`${height} rounded-full ${getBarClass(v)} transition-all duration-700 ease-out`}
          style={{ width: `${clamped}%` }}
        />
      </div>
      {label && (
        <span className="text-xs text-blox-muted tabular-nums font-mono w-10 text-right">
          {label}
        </span>
      )}
    </div>
  );
}
