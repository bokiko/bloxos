"use client";

import { useState, useEffect } from "react";
import { MachineMetrics } from "@/lib/demo-data";
import { ProgressBar } from "./ProgressBar";
import { StatusBadge, MachineStatus } from "./StatusBadge";
import { Cpu, HardDrive, MemoryStick, Thermometer, Clock, Monitor } from "lucide-react";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0";
  const gb = bytes / (1024 ** 3);
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / (1024 ** 2);
  return `${mb.toFixed(0)} MB`;
}

function getStatus(m: MachineMetrics): { status: MachineStatus; reason?: string } {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return { status: "offline" };

  if (m.gpu_temp && m.gpu_temp > 80) return { status: "warning", reason: `GPU ${m.gpu_temp}\u00b0C` };
  const diskPct = m.disk_total_bytes > 0 ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return { status: "warning", reason: `Disk ${diskPct.toFixed(0)}%` };
  const ramPct = m.ram_total_bytes > 0 ? (m.ram_used_bytes / m.ram_total_bytes) * 100 : 0;
  if (ramPct > 95) return { status: "warning", reason: `RAM ${ramPct.toFixed(0)}%` };

  return { status: "online" };
}

function timeSince(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ${hr % 24}h ago`;
}

const borderColors: Record<MachineStatus, string> = {
  online: "border-l-blox-green",
  warning: "border-l-blox-amber",
  offline: "border-l-blox-red",
};

interface MachineCardProps {
  machine: MachineMetrics;
  onClick?: () => void;
}

export function MachineCard({ machine, onClick }: MachineCardProps) {
  const [now, setNow] = useState(Date.now());
  const { status, reason } = getStatus(machine);

  useEffect(() => {
    const interval = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(interval);
  }, []);

  // Suppress unused var lint
  void now;

  const ramPct = machine.ram_total_bytes > 0
    ? (machine.ram_used_bytes / machine.ram_total_bytes) * 100
    : 0;
  const diskPct = machine.disk_total_bytes > 0
    ? (machine.disk_used_bytes / machine.disk_total_bytes) * 100
    : 0;

  const hasGpu = (machine.gpu_temp && machine.gpu_temp > 0) ||
    (machine.gpu_vram_total_bytes && machine.gpu_vram_total_bytes > 0);

  return (
    <div
      onClick={onClick}
      className={`
        bg-blox-card border-l-[3px] ${borderColors[status]}
        rounded-lg p-4 cursor-pointer
        border border-blox-border
        hover:border-blox-muted/30 hover:shadow-lg hover:shadow-black/20
        transition-all duration-200
      `}
    >
      {/* Header */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <Monitor className="w-4 h-4 text-blox-muted" />
          <span className="font-semibold text-sm text-blox-text">
            {machine.hostname}
          </span>
        </div>
        <StatusBadge status={status} reason={reason} />
      </div>

      {/* Metrics */}
      <div className="space-y-2.5">
        {/* CPU */}
        <div className="flex items-center gap-2">
          <Cpu className="w-3.5 h-3.5 text-blox-muted shrink-0" />
          <span className="text-xs text-blox-muted w-8">CPU</span>
          <div className="flex-1">
            <ProgressBar value={machine.cpu_percent} label={`${machine.cpu_percent.toFixed(0)}%`} />
          </div>
        </div>

        {/* RAM */}
        <div className="flex items-center gap-2">
          <MemoryStick className="w-3.5 h-3.5 text-blox-muted shrink-0" />
          <span className="text-xs text-blox-muted w-8">RAM</span>
          <div className="flex-1">
            <ProgressBar value={ramPct} label={`${ramPct.toFixed(0)}%`} />
          </div>
          <span className="text-[10px] text-blox-muted tabular-nums">
            {formatBytes(machine.ram_used_bytes)}/{formatBytes(machine.ram_total_bytes)}
          </span>
        </div>

        {/* Disk */}
        <div className="flex items-center gap-2">
          <HardDrive className="w-3.5 h-3.5 text-blox-muted shrink-0" />
          <span className="text-xs text-blox-muted w-8">Disk</span>
          <div className="flex-1">
            <ProgressBar value={diskPct} label={`${diskPct.toFixed(0)}%`} />
          </div>
          <span className="text-[10px] text-blox-muted tabular-nums">
            {formatBytes(machine.disk_used_bytes)}/{formatBytes(machine.disk_total_bytes)}
          </span>
        </div>

        {/* GPU Temp */}
        {hasGpu && machine.gpu_temp !== undefined && machine.gpu_temp > 0 && (
          <div className="flex items-center gap-2">
            <Thermometer className="w-3.5 h-3.5 text-blox-muted shrink-0" />
            <span className="text-xs text-blox-muted w-8">GPU</span>
            <span className={`text-xs tabular-nums ${
              machine.gpu_temp > 80 ? "text-blox-red" :
              machine.gpu_temp > 70 ? "text-blox-amber" : "text-blox-green"
            }`}>
              {machine.gpu_temp}\u00b0C
            </span>
            {machine.gpu_util_percent !== undefined && (
              <span className="text-[10px] text-blox-muted ml-1">
                ({machine.gpu_util_percent.toFixed(0)}% util)
              </span>
            )}
          </div>
        )}

        {/* VRAM */}
        {hasGpu && machine.gpu_vram_total_bytes && machine.gpu_vram_total_bytes > 0 && (
          <div className="flex items-center gap-2">
            <MemoryStick className="w-3.5 h-3.5 text-blox-muted shrink-0" />
            <span className="text-xs text-blox-muted w-8">VRAM</span>
            <div className="flex-1">
              <ProgressBar
                value={(machine.gpu_vram_used_bytes || 0) / machine.gpu_vram_total_bytes * 100}
                label={`${((machine.gpu_vram_used_bytes || 0) / machine.gpu_vram_total_bytes * 100).toFixed(0)}%`}
              />
            </div>
            <span className="text-[10px] text-blox-muted tabular-nums">
              {formatBytes(machine.gpu_vram_used_bytes || 0)}/{formatBytes(machine.gpu_vram_total_bytes)}
            </span>
          </div>
        )}
      </div>

      {/* Footer */}
      <div className="flex items-center justify-between mt-3 pt-2 border-t border-blox-border">
        <div className="flex items-center gap-1">
          <Clock className="w-3 h-3 text-blox-muted" />
          <span className="text-[10px] text-blox-muted">
            seen: {machine.last_seen ? timeSince(machine.last_seen) : "never"}
          </span>
        </div>
        {machine.os && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-blox-border text-blox-muted">
            {machine.os}
          </span>
        )}
      </div>
    </div>
  );
}
