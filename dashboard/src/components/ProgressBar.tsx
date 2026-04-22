"use client";

interface ProgressBarProps {
  value: number; // 0-100
  label?: string;
  size?: "sm" | "md";
}

function getBarColor(value: number): string {
  if (value < 60) return "from-emerald-500 to-emerald-400";
  if (value <= 85) return "from-amber-500 to-amber-400";
  return "from-red-500 to-red-400";
}

function getBarBg(value: number): string {
  if (value < 60) return "bg-emerald-500/10";
  if (value <= 85) return "bg-amber-500/10";
  return "bg-red-500/10";
}

export function ProgressBar({ value, label, size = "sm" }: ProgressBarProps) {
  const clamped = Math.min(100, Math.max(0, value));
  const height = size === "sm" ? "h-1.5" : "h-2";

  return (
    <div className="flex items-center gap-3">
      <div className={`flex-1 ${height} rounded-full ${getBarBg(clamped)} overflow-hidden`}>
        <div
          className={`${height} rounded-full bg-gradient-to-r ${getBarColor(clamped)} transition-all duration-700 ease-out`}
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
