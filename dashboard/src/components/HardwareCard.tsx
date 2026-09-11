"use client";

// Static hardware inventory for one machine.
//
// Monoform: a single panel with a header strip and four hairline-separated
// sections. Nothing here is live, so nothing here is coloured — the whole
// card is text hierarchy, with mono reserved for identifiers and sizes.

import { Cpu, MemoryStick, HardDrive, Network, Server, Zap } from "lucide-react";
import { MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";

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
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <h2 className={MF_PANEL_TITLE}>Hardware profile</h2>
        <span className="mf-kicker">as reported by the agent</span>
      </div>

      {/* Two columns at md; the nth-child rule restores the horizontal rule
          between the two rows, which `divide-y-0` drops. */}
      <div className="mf-machine-hardware-grid">
        {/* SECTION 1: Compute */}
        <Section icon={<Cpu className="w-3.5 h-3.5" />} title="Compute">
          <PrimaryField label="Processor" value={hw.cpu_model || "—"} />
          {cpuDetail && <SecondaryLine>{cpuDetail}</SecondaryLine>}
          {hw.cpu_vendor && <SecondaryLine>{hw.cpu_vendor}</SecondaryLine>}

          {gpuNames.length > 0 && (
            <div className="mt-4 space-y-1.5 border-t border-border-subtle pt-4">
              <div className="mf-kicker flex items-center gap-1.5 uppercase">
                <Zap className="w-2.5 h-2.5" aria-hidden />
                Graphics
              </div>
              {/* Keyed by position, not by name: a box with two identical
                  cards reports the same model string twice, which is the
                  normal case rather than an edge one. */}
              {gpuNames.map((name, index) => (
                <div key={index} className="font-mono text-xs leading-snug text-text-primary">
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
            <div className="mt-4 space-y-2.5 border-t border-border-subtle pt-4">
              <div className="mf-kicker flex items-center gap-1.5 uppercase">
                <HardDrive className="w-2.5 h-2.5" aria-hidden />
                Disks ({disks.length})
              </div>
              {disks.map((d) => (
                <div
                  key={d.device}
                  className="flex items-baseline justify-between gap-3 text-xs leading-tight"
                >
                  <div className="min-w-0 flex-1">
                    <span className="font-mono text-text-primary">
                      {d.device.replace(/^\/dev\//, "")}
                    </span>
                    {d.type && (
                      <span className="ml-2 font-mono text-[10px] uppercase tracking-[0.05em] text-text-tertiary">
                        {d.type}
                      </span>
                    )}
                    {d.model && (
                      <div className="mt-0.5 truncate text-[10px] text-text-tertiary">{d.model}</div>
                    )}
                  </div>
                  <span className="mf-metric shrink-0 text-text-primary">
                    {formatBytes(d.size_bytes)}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <SecondaryLine className="mt-4 border-t border-border-subtle pt-4">
              No disks reported
            </SecondaryLine>
          )}
        </Section>

        {/* SECTION 3: Network */}
        <Section icon={<Network className="w-3.5 h-3.5" />} title="Network">
          {nics.length > 0 ? (
            <div className="space-y-2.5">
              {nics.map((n) => (
                <div key={n.name} className="text-xs leading-tight">
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="font-mono text-text-primary">{n.name}</span>
                    {formatNICSpeed(n.speed_mbps) && (
                      <span className="mf-metric shrink-0 text-[10px] text-text-tertiary">
                        {formatNICSpeed(n.speed_mbps)}
                      </span>
                    )}
                  </div>
                  {n.ipv4 && (
                    <div className="mf-metric mt-0.5 text-[10px] text-text-tertiary">{n.ipv4}</div>
                  )}
                  {n.mac && (
                    <div className="mt-0.5 font-mono text-[10px] text-text-disabled">{n.mac}</div>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <SecondaryLine>No network interfaces reported</SecondaryLine>
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
    </section>
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
    <div className="px-6 py-5">
      <h3 className="mf-kicker mb-3.5 flex items-center gap-1.5 uppercase">
        <span aria-hidden>{icon}</span>
        {title}
      </h3>
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
      <div className="mf-kicker uppercase">{label}</div>
      <div className="mt-1.5 text-[13px] font-medium leading-snug text-text-primary">{value}</div>
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
    <div className={`text-[11px] leading-snug text-text-tertiary ${className}`}>{children}</div>
  );
}

interface DetailRowProps {
  label: string;
  value: string | number | null | undefined;
}

function DetailRow({ label, value }: DetailRowProps) {
  if (value === null || value === undefined || value === "" || value === 0) return null;
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-border-subtle py-2 first:pt-0 last:border-b-0 last:pb-0">
      <span className="mf-kicker">{label}</span>
      <span className="mf-metric truncate text-right text-xs text-text-primary">{value}</span>
    </div>
  );
}
