"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import { MachineMetrics } from "@/lib/demo-data";
import { ProgressBar } from "./ProgressBar";
import { StatusBadge, MachineStatus } from "./StatusBadge";
import { Sparkline } from "./Sparkline";
import { Cpu, HardDrive, MemoryStick, Thermometer, Clock, Monitor, Wifi, Trash2, Pencil } from "lucide-react";
import { motion } from "framer-motion";

function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes === 0) return "0";
  const gb = bytes / (1024 ** 3);
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / (1024 ** 2);
  return `${mb.toFixed(0)} MB`;
}

function getStatus(m: MachineMetrics): { status: MachineStatus; reason?: string } {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return { status: "offline" };

  if (m.gpu_temp && m.gpu_temp > 80) return { status: "warning", reason: `GPU ${m.gpu_temp}\u00b0C` };
  const diskPct = (m.disk_total_bytes ?? 0) > 0 ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return { status: "warning", reason: `Disk ${diskPct.toFixed(0)}%` };
  const ramPct = (m.ram_total_bytes ?? 0) > 0 ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100 : 0;
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

const statusBorderColor: Record<MachineStatus, string> = {
  online: "border-l-emerald-500",
  warning: "border-l-amber-500",
  offline: "border-l-red-500/50",
};

const statusGlow: Record<MachineStatus, string> = {
  online: "hover:shadow-emerald-500/5",
  warning: "hover:shadow-amber-500/5",
  offline: "hover:shadow-red-500/5",
};

const API_ADAPTER_TYPES = ["synology", "proxmox"];

interface MachineCardProps {
  machine: MachineMetrics;
  onClick?: () => void;
  onDelete?: (machineId: string, hostname: string) => void;
  onEdit?: (machineId: string) => void;
}

export function MachineCard({ machine, onClick, onDelete, onEdit }: MachineCardProps) {
  const [now, setNow] = useState(() => Date.now());
  const { status, reason } = getStatus(machine);

  useEffect(() => {
    const interval = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(interval);
  }, []);

  void now;

  const ramPct = (machine.ram_total_bytes ?? 0) > 0
    ? ((machine.ram_used_bytes ?? 0) / machine.ram_total_bytes) * 100
    : 0;
  const diskPct = (machine.disk_total_bytes ?? 0) > 0
    ? ((machine.disk_used_bytes ?? 0) / machine.disk_total_bytes) * 100
    : 0;

  const gpu0 = machine.gpus && machine.gpus.length > 0 ? machine.gpus[0] : null;
  const gpuVramUsed = gpu0 ? gpu0.mem_used_bytes : (machine.gpu_vram_used_bytes || 0);
  const gpuVramTotal = gpu0 ? gpu0.mem_total_bytes : (machine.gpu_vram_total_bytes || 0);
  const hasGpuTemp = (machine.gpu_temp ?? 0) > 0;
  const hasGpuVram = gpuVramTotal > 0;

  const tags = machine.tags ? machine.tags.split(",").filter((t) => t.trim()) : [];
  const adapterTag = tags.find((t) => API_ADAPTER_TYPES.includes(t.trim().toLowerCase()));
  const isAPIMachine = !!adapterTag;

  return (
    <Link href={`/machine/${machine.machine_id}`} className="block h-full">
      <motion.div
        whileHover={{ scale: 1.02 }}
        transition={{ type: "spring", stiffness: 400, damping: 25 }}
        onClick={onClick}
        className={`
          group bg-blox-card border-l-[3px] ${statusBorderColor[status]}
          rounded-xl p-5 cursor-pointer flex flex-col h-full
          border border-blox-border
          hover:border-blox-muted/20 hover:shadow-xl ${statusGlow[status]}
          transition-all duration-300
        `}
      >
        {/* Header */}
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-border/50">
              <Monitor className="w-3.5 h-3.5 text-blox-muted" />
            </div>
            <span className="font-semibold text-sm text-blox-text tracking-tight">
              {machine.hostname}
            </span>
            {isAPIMachine && adapterTag && (
              <span className="text-[9px] px-1.5 py-0.5 rounded-md bg-violet-500/10 text-violet-400 border border-violet-500/20 font-semibold uppercase tracking-wider">
                {adapterTag.trim()}
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge status={status} reason={reason} />
            {isAPIMachine && onEdit && (
              <button
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onEdit(machine.machine_id);
                }}
                className="opacity-0 group-hover:opacity-100 p-1.5 rounded-lg hover:bg-blox-blue/10 text-blox-muted hover:text-blox-blue transition-all duration-200"
                title="Edit API machine"
              >
                <Pencil className="w-3.5 h-3.5" />
              </button>
            )}
            {onDelete && (
              <button
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onDelete(machine.machine_id, machine.hostname);
                }}
                className="opacity-0 group-hover:opacity-100 p-1.5 rounded-lg hover:bg-red-500/10 text-blox-muted hover:text-red-400 transition-all duration-200"
                title="Delete machine"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            )}
          </div>
        </div>

        {/* Sparkline */}
        {status !== "offline" && (
          <div className="mb-3 flex items-center gap-2">
            <span className="text-[9px] text-blox-muted uppercase tracking-wider font-medium">CPU 30m</span>
            <Sparkline machineId={machine.machine_id} width={100} height={24} />
          </div>
        )}

        {/* Metrics */}
        <div className="space-y-3">
          {/* CPU */}
          <div className="flex items-center gap-2">
            <Cpu className="w-3.5 h-3.5 text-blox-muted shrink-0" />
            <span className="text-xs text-blox-muted w-8">CPU</span>
            <div className="flex-1">
              <ProgressBar value={machine.cpu_percent ?? 0} label={`${(machine.cpu_percent ?? 0).toFixed(0)}%`} />
            </div>
          </div>

          {/* RAM */}
          <div className="flex items-center gap-2">
            <MemoryStick className="w-3.5 h-3.5 text-blox-muted shrink-0" />
            <span className="text-xs text-blox-muted w-8">RAM</span>
            <div className="flex-1">
              <ProgressBar value={ramPct} label={`${ramPct.toFixed(0)}%`} />
            </div>
            <span className="text-[10px] text-blox-muted tabular-nums font-mono">
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
            <span className="text-[10px] text-blox-muted tabular-nums font-mono">
              {formatBytes(machine.disk_used_bytes)}/{formatBytes(machine.disk_total_bytes)}
            </span>
          </div>

          {/* GPU Temp — rendered only when the machine actually has a GPU. */}
          {hasGpuTemp && (
            <div className="flex items-center gap-2">
              <Thermometer className="w-3.5 h-3.5 text-blox-muted shrink-0" />
              <span className="text-xs text-blox-muted w-8">GPU</span>
              <span className={`text-xs tabular-nums font-mono font-medium ${
                (machine.gpu_temp ?? 0) > 80 ? "text-red-400" :
                (machine.gpu_temp ?? 0) > 60 ? "text-amber-400" : "text-emerald-400"
              }`}>
                {machine.gpu_temp}&deg;C
              </span>
              {machine.gpu_util_percent !== undefined && (
                <span className="text-[10px] text-blox-muted tabular-nums font-mono ml-1">
                  ({(machine.gpu_util_percent ?? 0).toFixed(0)}% util)
                </span>
              )}
            </div>
          )}

          {/* VRAM — rendered only when total VRAM is known. */}
          {hasGpuVram && (
            <div className="flex items-center gap-2">
              <MemoryStick className="w-3.5 h-3.5 text-blox-muted shrink-0" />
              <span className="text-xs text-blox-muted w-8">VRAM</span>
              <div className="flex-1">
                <ProgressBar
                  value={(gpuVramUsed / gpuVramTotal) * 100}
                  label={`${((gpuVramUsed / gpuVramTotal) * 100).toFixed(0)}%`}
                />
              </div>
              <span className="text-[10px] text-blox-muted tabular-nums font-mono">
                {formatBytes(gpuVramUsed)}/{formatBytes(gpuVramTotal)}
              </span>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between mt-auto pt-3 border-t border-blox-border/50">
          <div className="flex items-center gap-1.5">
            <Clock className="w-3 h-3 text-blox-muted" />
            <span className="text-[10px] text-blox-muted font-mono tabular-nums">
              {machine.last_seen ? timeSince(machine.last_seen) : "never"}
            </span>
          </div>
          <div className="flex items-center gap-2">
            {machine.latency_ms !== undefined && machine.latency_ms > 0 && (
              <span className="flex items-center gap-0.5 text-[10px] text-blox-muted font-mono tabular-nums">
                <Wifi className="w-2.5 h-2.5" />
                {machine.latency_ms}ms
              </span>
            )}
            {machine.os && (
              <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-blox-border/50 text-blox-muted font-mono">
                {machine.os}
              </span>
            )}
          </div>
        </div>

        {/* Tags */}
        {tags.length > 0 && (
          <div className="flex gap-1.5 flex-wrap mt-2.5 pt-2.5 border-t border-blox-border/50">
            {tags.map((tag) => (
              <span key={tag} className="text-[9px] px-2 py-0.5 rounded-full bg-blox-blue/10 text-blox-blue/80 border border-blox-blue/20 font-medium">
                {tag.trim()}
              </span>
            ))}
          </div>
        )}
      </motion.div>
    </Link>
  );
}
