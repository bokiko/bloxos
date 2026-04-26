"use client";

import { motion } from "framer-motion";
import { Cpu, MemoryStick, HardDrive, Network, Server, Zap } from "lucide-react";

/* ============================================================================
 * Types
 * ============================================================================ */

interface DiskInfo {
  device: string;
  model?: string;
  size_bytes?: number;
  type?: string;
}

interface NetworkInfo {
  name: string;
  mac?: string;
  ipv4?: string;
  speed_mbps?: number;
}

export interface HardwareInfo {
  cpu_model?: string;
  cpu_vendor?: string;
  cpu_cores?: number;
  cpu_threads?: number;
  cpu_frequency_mhz?: number;
  ram_total_bytes?: number;
  kernel_version?: string;
  platform_family?: string;
  virtualization?: string;
  boot_time?: number;
  architecture?: string;
  gpu_models?: string[];
  disks?: DiskInfo[];
  network_interfaces?: NetworkInfo[];
}

/* ============================================================================
 * Helpers
 * ============================================================================ */

function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes <= 0) return "";
  const tb = bytes / 1024 ** 4;
  if (tb >= 1) return `${tb.toFixed(tb >= 10 ? 0 : 1)} TB`;
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / 1024 ** 2;
  return `${mb.toFixed(0)} MB`;
}

function formatUptime(bootUnix: number | undefined): string {
  if (!bootUnix || bootUnix <= 0) return "";
  const secs = Math.max(0, Math.floor(Date.now() / 1000 - bootUnix));
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  const m = Math.floor((secs % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function formatNICSpeed(mbps?: number): string {
  if (!mbps || mbps <= 0) return "";
  if (mbps >= 1000) {
    const gbps = mbps / 1000;
    return `${gbps % 1 === 0 ? gbps.toFixed(0) : gbps.toFixed(1)} Gb/s`;
  }
  return `${mbps} Mb/s`;
}

/* ============================================================================
 * Public component
 * ============================================================================ */

interface HardwareCardProps {
  hw: HardwareInfo;
}

export function HardwareCard({ hw }: HardwareCardProps) {
  const cpuDetail = [
    hw.cpu_cores ? `${hw.cpu_cores} cores` : "",
    hw.cpu_threads && hw.cpu_threads !== hw.cpu_cores ? `${hw.cpu_threads} threads` : "",
    hw.cpu_frequency_mhz && hw.cpu_frequency_mhz > 0
      ? `${(hw.cpu_frequency_mhz / 1000).toFixed(1)} GHz`
      : "",
  ]
    .filter(Boolean)
    .join(" · ");

  const uptime = formatUptime(hw.boot_time);
  const gpuNames = (hw.gpu_models ?? []).filter(Boolean);
  const disks = (hw.disks ?? []).filter((d) => (d.size_bytes ?? 0) > 0 || d.model);
  const nics = (hw.network_interfaces ?? []).filter((n) => n.name);

  return (
    <motion.div
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: 0.15 }}
      className="bg-blox-card border border-blox-border rounded-xl overflow-hidden mt-6"
    >
      <div className="flex items-center gap-2.5 px-5 py-4 border-b border-blox-border/50">
        <div className="p-1.5 rounded-lg bg-blox-blue/10">
          <Server className="w-3.5 h-3.5 text-blox-blue" />
        </div>
        <h3 className="text-sm font-semibold text-blox-text">Hardware</h3>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 divide-y md:divide-y-0 md:divide-x divide-blox-border/40">
        {/* SECTION 1: Compute */}
        <Section icon={<Cpu className="w-3.5 h-3.5" />} title="Compute">
          <PrimaryField label="Processor" value={hw.cpu_model || "—"} />
          {cpuDetail && <SecondaryLine>{cpuDetail}</SecondaryLine>}
          {hw.cpu_vendor && <SecondaryLine>{hw.cpu_vendor}</SecondaryLine>}

          {gpuNames.length > 0 && (
            <div className="pt-3 mt-3 border-t border-blox-border/30 space-y-1">
              <div className="flex items-center gap-1.5 text-[9px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium">
                <Zap className="w-2.5 h-2.5" />
                Graphics
              </div>
              {gpuNames.map((name) => (
                <div key={name} className="text-xs text-blox-text font-mono leading-snug">
                  {name}
                </div>
              ))}
            </div>
          )}
        </Section>

        {/* SECTION 2: Memory & Storage */}
        <Section icon={<MemoryStick className="w-3.5 h-3.5" />} title="Memory & Storage">
          <PrimaryField
            label="Memory"
            value={hw.ram_total_bytes ? formatBytes(hw.ram_total_bytes) : "—"}
          />

          {disks.length > 0 ? (
            <div className="pt-3 mt-3 border-t border-blox-border/30 space-y-2">
              <div className="flex items-center gap-1.5 text-[9px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium">
                <HardDrive className="w-2.5 h-2.5" />
                Disks ({disks.length})
              </div>
              {disks.map((d) => (
                <div
                  key={d.device}
                  className="flex items-baseline justify-between gap-3 text-xs leading-tight"
                >
                  <div className="min-w-0 flex-1">
                    <span className="text-blox-text font-mono">
                      {d.device.replace(/^\/dev\//, "")}
                    </span>
                    {d.type && (
                      <span className="ml-2 text-[9px] uppercase tracking-wider text-blox-muted/70">
                        {d.type}
                      </span>
                    )}
                    {d.model && (
                      <div className="text-[10px] text-blox-muted/80 mt-0.5 truncate">
                        {d.model}
                      </div>
                    )}
                  </div>
                  <span className="text-blox-text font-mono tabular-nums shrink-0">
                    {formatBytes(d.size_bytes)}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <SecondaryLine className="pt-3 mt-3 border-t border-blox-border/30 text-blox-muted/50">
              No disks reported
            </SecondaryLine>
          )}
        </Section>

        {/* SECTION 3: Network */}
        <Section icon={<Network className="w-3.5 h-3.5" />} title="Network">
          {nics.length > 0 ? (
            <div className="space-y-2">
              {nics.map((n) => (
                <div key={n.name} className="text-xs leading-tight">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="text-blox-text font-mono">{n.name}</span>
                    {formatNICSpeed(n.speed_mbps) && (
                      <span className="text-[10px] text-blox-muted/80 font-mono shrink-0">
                        {formatNICSpeed(n.speed_mbps)}
                      </span>
                    )}
                  </div>
                  {n.ipv4 && (
                    <div className="text-[10px] text-blox-muted/80 font-mono tabular-nums mt-0.5">
                      {n.ipv4}
                    </div>
                  )}
                  {n.mac && (
                    <div className="text-[10px] text-blox-muted/50 font-mono mt-0.5">
                      {n.mac}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <SecondaryLine className="text-blox-muted/50">
              No network interfaces reported
            </SecondaryLine>
          )}
        </Section>

        {/* SECTION 4: Platform */}
        <Section icon={<Server className="w-3.5 h-3.5" />} title="Platform">
          <DetailRow label="Architecture" value={hw.architecture} />
          <DetailRow label="Kernel" value={hw.kernel_version} />
          <DetailRow label="Platform" value={hw.platform_family} />
          <DetailRow
            label="Virtualization"
            value={hw.virtualization && hw.virtualization !== "" ? hw.virtualization : "—"}
          />
          <DetailRow label="Uptime" value={uptime} />
        </Section>
      </div>
    </motion.div>
  );
}

/* ============================================================================
 * Subcomponents
 * ============================================================================ */

interface SectionProps {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}

function Section({ icon, title, children }: SectionProps) {
  return (
    <div className="px-5 py-4">
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.1em] text-blox-muted font-medium mb-3">
        <span className="text-blox-muted/80">{icon}</span>
        {title}
      </div>
      <div>{children}</div>
    </div>
  );
}

interface PrimaryFieldProps {
  label: string;
  value: string;
}

function PrimaryField({ label, value }: PrimaryFieldProps) {
  return (
    <div>
      <div className="text-[9px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium">
        {label}
      </div>
      <div className="text-sm text-blox-text font-medium mt-1 leading-snug">{value}</div>
    </div>
  );
}

function SecondaryLine({
  children,
  className = "",
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={`text-[11px] text-blox-muted leading-snug ${className}`}>{children}</div>
  );
}

interface DetailRowProps {
  label: string;
  value: string | number | null | undefined;
}

function DetailRow({ label, value }: DetailRowProps) {
  if (value === null || value === undefined || value === "" || value === 0) return null;
  return (
    <div className="flex items-baseline justify-between gap-3 py-1.5 border-b border-blox-border/30 last:border-b-0 first:pt-0 last:pb-0">
      <span className="text-[11px] text-blox-muted">{label}</span>
      <span className="text-xs text-blox-text font-mono tabular-nums text-right truncate">
        {value}
      </span>
    </div>
  );
}
