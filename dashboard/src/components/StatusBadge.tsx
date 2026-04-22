"use client";

import { Badge } from "@/components/ui/badge";
import { AlertTriangle, Circle } from "lucide-react";

export type MachineStatus = "online" | "warning" | "offline";

interface StatusBadgeProps {
  status: MachineStatus;
  reason?: string;
}

export function StatusBadge({ status, reason }: StatusBadgeProps) {
  if (status === "online") {
    return (
      <Badge variant="outline" className="gap-1.5 border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px] px-2 py-0.5 h-auto">
        <span className="relative flex h-2 w-2">
          <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
          <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
        </span>
        Online
        {reason && <span className="text-blox-muted ml-0.5">({reason})</span>}
      </Badge>
    );
  }

  if (status === "warning") {
    return (
      <Badge variant="outline" className="gap-1 border-amber-500/30 bg-amber-500/10 text-amber-400 text-[10px] px-2 py-0.5 h-auto">
        <AlertTriangle className="w-2.5 h-2.5" />
        Warning
        {reason && <span className="text-blox-muted ml-0.5">({reason})</span>}
      </Badge>
    );
  }

  return (
    <Badge variant="outline" className="gap-1 border-red-500/30 bg-red-500/10 text-red-400 text-[10px] px-2 py-0.5 h-auto">
      <Circle className="w-2.5 h-2.5" />
      Offline
    </Badge>
  );
}
