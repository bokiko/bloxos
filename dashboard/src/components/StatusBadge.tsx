"use client";

export type MachineStatus = "online" | "warning" | "offline";

interface StatusBadgeProps {
  status: MachineStatus;
  reason?: string;
}

const config: Record<MachineStatus, { dot: string; color: string; label: string }> = {
  online: { dot: "\u25cf", color: "text-blox-green", label: "Online" },
  warning: { dot: "\u26a0", color: "text-blox-amber", label: "Warning" },
  offline: { dot: "\u25cb", color: "text-blox-red", label: "Offline" },
};

export function StatusBadge({ status, reason }: StatusBadgeProps) {
  const { dot, color, label } = config[status];
  return (
    <div className="flex items-center gap-1.5">
      <span className={`${color} text-sm`}>{dot}</span>
      <span className={`text-xs ${color}`}>
        {label}
        {reason && <span className="text-blox-muted ml-1">({reason})</span>}
      </span>
    </div>
  );
}
