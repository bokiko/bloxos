"use client";

interface ProgressBarProps {
  value: number; // 0-100
  label?: string;
  size?: "sm" | "md";
}

function getBarColor(value: number): string {
  if (value < 60) return "bg-blox-green";
  if (value <= 85) return "bg-blox-amber";
  return "bg-blox-red";
}

export function ProgressBar({ value, label, size = "sm" }: ProgressBarProps) {
  const clamped = Math.min(100, Math.max(0, value));
  const height = size === "sm" ? "h-1.5" : "h-2";

  return (
    <div className="flex items-center gap-3">
      <div className={`flex-1 ${height} rounded-full bg-blox-border overflow-hidden`}>
        <div
          className={`${height} rounded-full transition-all duration-500 ${getBarColor(clamped)}`}
          style={{ width: `${clamped}%` }}
        />
      </div>
      {label && (
        <span className="text-xs text-blox-muted tabular-nums w-10 text-right">
          {label}
        </span>
      )}
    </div>
  );
}
