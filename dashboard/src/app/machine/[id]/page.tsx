"use client";

import { useEffect, useState, useCallback, useRef, use } from "react";
import Link from "next/link";
import { ProgressBar } from "@/components/ProgressBar";
import { StatusBadge, MachineStatus } from "@/components/StatusBadge";
import { ServicePanel, Service } from "@/components/ServicePanel";
import { ContainerPanel, Container } from "@/components/ContainerPanel";
import { RebootModal } from "@/components/RebootModal";
import {
  ArrowLeft, Cpu, HardDrive, MemoryStick, Thermometer,
  Activity, Network, Zap, Monitor, Terminal, RotateCcw,
  Lock,
} from "lucide-react";

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

interface MachineData {
  machine: {
    id: string;
    hostname: string;
    ip: string | null;
    os: string | null;
    status: string;
    last_seen: string | null;
  };
  metrics: {
    cpu_percent: number;
    ram_used_bytes: number;
    ram_total_bytes: number;
    disk_used_bytes: number;
    disk_total_bytes: number;
    gpu_temp: number;
    gpu_util_percent: number;
    gpu_vram_used_bytes: number;
    gpu_vram_total_bytes: number;
  };
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0";
  const gb = bytes / (1024 ** 3);
  if (gb >= 1) return `${gb.toFixed(1)} GB`;
  const mb = bytes / (1024 ** 2);
  return `${mb.toFixed(0)} MB`;
}

function timeSince(dateStr: string): string {
  const sec = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

function getStatus(data: MachineData): MachineStatus {
  if (data.machine.status === "offline") return "offline";
  if (data.metrics.gpu_temp > 80) return "warning";
  const diskPct = data.metrics.disk_total_bytes > 0
    ? (data.metrics.disk_used_bytes / data.metrics.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return "warning";
  return "online";
}

export default function MachineDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const [data, setData] = useState<MachineData | null>(null);
  const [services, setServices] = useState<Service[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [showReboot, setShowReboot] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  // Fetch initial data (machine + services + containers)
  const fetchData = useCallback(async () => {
    try {
      const [machineRes, servicesRes, containersRes] = await Promise.all([
        fetch(`${HUB_URL}/api/machines/${id}`),
        fetch(`${HUB_URL}/api/machines/${id}/services`),
        fetch(`${HUB_URL}/api/machines/${id}/containers`),
      ]);

      if (!machineRes.ok) {
        setError(machineRes.status === 404 ? "Machine not found" : "Failed to load");
        return;
      }

      const machineJson = await machineRes.json();
      setData(machineJson);
      setLastUpdated(new Date().toISOString());
      setError(null);

      if (servicesRes.ok) {
        const svcJson = await servicesRes.json();
        setServices(svcJson);
      }
      if (containersRes.ok) {
        const ctrJson = await containersRes.json();
        setContainers(ctrJson);
      }
    } catch {
      setError("Cannot reach hub");
    }
  }, [id]);

  // SSE for real-time updates
  useEffect(() => {
    fetchData();

    const es = new EventSource(`${HUB_URL}/api/events`);
    esRef.current = es;

    es.addEventListener("snapshot", (event) => {
      try {
        const list = JSON.parse(event.data);
        const machine = list.find((m: { machine_id: string }) => m.machine_id === id);
        if (machine) {
          if (machine.services) setServices(machine.services);
          if (machine.containers) setContainers(machine.containers);
        }
      } catch { /* ignore */ }
    });

    es.addEventListener("metrics", (event) => {
      try {
        const m = JSON.parse(event.data);
        if (m.machine_id === id) {
          setData((prev) => {
            if (!prev) return prev;
            return {
              ...prev,
              metrics: {
                cpu_percent: m.cpu_percent ?? prev.metrics.cpu_percent,
                ram_used_bytes: m.ram_used_bytes ?? prev.metrics.ram_used_bytes,
                ram_total_bytes: m.ram_total_bytes ?? prev.metrics.ram_total_bytes,
                disk_used_bytes: m.disk_used_bytes ?? prev.metrics.disk_used_bytes,
                disk_total_bytes: m.disk_total_bytes ?? prev.metrics.disk_total_bytes,
                gpu_temp: m.gpu_temp ?? prev.metrics.gpu_temp,
                gpu_util_percent: m.gpu_util_percent ?? prev.metrics.gpu_util_percent,
                gpu_vram_used_bytes: m.gpu_vram_used_bytes ?? prev.metrics.gpu_vram_used_bytes,
                gpu_vram_total_bytes: m.gpu_vram_total_bytes ?? prev.metrics.gpu_vram_total_bytes,
              },
              machine: { ...prev.machine, status: "online" },
            };
          });
          setLastUpdated(new Date().toISOString());
        }
      } catch { /* ignore */ }
    });

    // Listen for services/containers SSE events
    es.addEventListener("services", (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.machine_id === id && msg.services) {
          setServices(msg.services);
        }
      } catch { /* ignore */ }
    });

    es.addEventListener("containers", (event) => {
      try {
        const msg = JSON.parse(event.data);
        if (msg.machine_id === id && msg.containers) {
          setContainers(msg.containers);
        }
      } catch { /* ignore */ }
    });

    es.onerror = () => {
      es.close();
      setTimeout(() => {
        if (esRef.current === es) fetchData();
      }, 3000);
    };

    return () => {
      es.close();
      esRef.current = null;
    };
  }, [id, fetchData]);

  // Tick every second for "last updated" display
  useEffect(() => {
    const interval = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(interval);
  }, []);
  void tick;

  if (error && !data) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="text-center">
          <p className="text-blox-red text-sm mb-4">{error}</p>
          <Link href="/" className="text-blox-blue text-sm hover:underline">Back to Fleet</Link>
        </div>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="text-blox-muted text-sm">Loading...</div>
      </div>
    );
  }

  const { machine, metrics } = data;
  const status = getStatus(data);
  const ramPct = metrics.ram_total_bytes > 0 ? (metrics.ram_used_bytes / metrics.ram_total_bytes) * 100 : 0;
  const diskPct = metrics.disk_total_bytes > 0 ? (metrics.disk_used_bytes / metrics.disk_total_bytes) * 100 : 0;
  const gpuVramPct = metrics.gpu_vram_total_bytes > 0 ? (metrics.gpu_vram_used_bytes / metrics.gpu_vram_total_bytes) * 100 : 0;
  const hasGpu = metrics.gpu_temp > 0 || metrics.gpu_vram_total_bytes > 0;

  return (
    <div className="min-h-screen bg-blox-bg">
      {/* Reboot confirmation modal */}
      {showReboot && (
        <RebootModal
          hostname={machine.hostname}
          machineId={id}
          hubUrl={HUB_URL}
          onClose={() => setShowReboot(false)}
        />
      )}

      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-md border-b border-blox-border">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link href="/" className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-sm">
              <ArrowLeft className="w-4 h-4" />
              Fleet
            </Link>
            <span className="text-blox-border">/</span>
            <div className="flex items-center gap-2">
              <Monitor className="w-4 h-4 text-blox-muted" />
              <h1 className="font-semibold text-blox-text">{machine.hostname}</h1>
              <StatusBadge status={status} />
            </div>
          </div>
          <div className="flex items-center gap-2">
            {lastUpdated && (
              <span className="text-[10px] text-blox-muted mr-2">
                updated: {timeSince(lastUpdated)}
              </span>
            )}
            <button
              onClick={() => setShowReboot(true)}
              disabled={machine.status === "offline"}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border transition-colors ${
                machine.status === "offline"
                  ? "border-blox-border text-blox-muted opacity-50 cursor-not-allowed"
                  : "border-blox-border text-blox-text hover:border-blox-red/40 hover:text-blox-red hover:bg-blox-red/5"
              }`}
            >
              <RotateCcw className="w-3.5 h-3.5" />
              Reboot
            </button>
            <button
              disabled
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border border-blox-border text-blox-muted opacity-50 cursor-not-allowed"
            >
              <Terminal className="w-3.5 h-3.5" />
              Terminal
            </button>
          </div>
        </div>
      </header>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">
        {/* Machine info bar */}
        <div className="flex flex-wrap gap-4 text-xs text-blox-muted">
          {machine.ip && <span>IP: <span className="text-blox-text">{machine.ip}</span></span>}
          {machine.os && <span>OS: <span className="text-blox-text">{machine.os}</span></span>}
          <span>ID: <span className="text-blox-text font-mono text-[10px]">{machine.id}</span></span>
        </div>

        {/* Two-column grid: System + GPU */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Left: System panel */}
          <div className="bg-blox-card border border-blox-border rounded-lg p-5">
            <div className="flex items-center gap-2 mb-5">
              <Activity className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">System</h3>
            </div>
            <div className="space-y-4">
              {/* CPU */}
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <Cpu className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">CPU</span>
                  </div>
                  <span className="text-sm font-semibold text-blox-text tabular-nums">{metrics.cpu_percent.toFixed(1)}%</span>
                </div>
                <ProgressBar value={metrics.cpu_percent} size="md" />
              </div>

              {/* RAM */}
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <MemoryStick className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">RAM</span>
                  </div>
                  <span className="text-xs text-blox-muted tabular-nums">{formatBytes(metrics.ram_used_bytes)} / {formatBytes(metrics.ram_total_bytes)}</span>
                </div>
                <ProgressBar value={ramPct} size="md" label={`${ramPct.toFixed(0)}%`} />
              </div>

              {/* Disk */}
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <HardDrive className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">Disk</span>
                  </div>
                  <span className="text-xs text-blox-muted tabular-nums">{formatBytes(metrics.disk_used_bytes)} / {formatBytes(metrics.disk_total_bytes)}</span>
                </div>
                <ProgressBar value={diskPct} size="md" label={`${diskPct.toFixed(0)}%`} />
              </div>

              {/* Network placeholder */}
              <div className="flex items-center justify-between py-2 border-t border-blox-border">
                <div className="flex items-center gap-2">
                  <Network className="w-3.5 h-3.5 text-blox-muted" />
                  <span className="text-xs text-blox-muted">Network</span>
                </div>
                <span className="text-[10px] text-blox-muted">Phase 2</span>
              </div>

              {/* Load placeholder */}
              <div className="flex items-center justify-between py-2 border-t border-blox-border">
                <div className="flex items-center gap-2">
                  <Activity className="w-3.5 h-3.5 text-blox-muted" />
                  <span className="text-xs text-blox-muted">Load Average</span>
                </div>
                <span className="text-[10px] text-blox-muted">Phase 2</span>
              </div>
            </div>
          </div>

          {/* Right: GPU panel */}
          <div className="bg-blox-card border border-blox-border rounded-lg p-5">
            <div className="flex items-center gap-2 mb-5">
              <Zap className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">GPU</h3>
              {!hasGpu && <span className="text-[10px] text-blox-muted">(no GPU detected)</span>}
            </div>
            {hasGpu ? (
              <div className="space-y-4">
                {/* Temperature */}
                <div>
                  <div className="flex items-center justify-between mb-1.5">
                    <div className="flex items-center gap-2">
                      <Thermometer className="w-3.5 h-3.5 text-blox-muted" />
                      <span className="text-xs text-blox-muted">Temperature</span>
                    </div>
                    <span className={`text-sm font-semibold tabular-nums ${
                      metrics.gpu_temp > 80 ? "text-blox-red" :
                      metrics.gpu_temp > 70 ? "text-blox-amber" : "text-blox-green"
                    }`}>{metrics.gpu_temp}&deg;C</span>
                  </div>
                  <ProgressBar value={metrics.gpu_temp} size="md" />
                </div>

                {/* Utilization */}
                <div>
                  <div className="flex items-center justify-between mb-1.5">
                    <div className="flex items-center gap-2">
                      <Cpu className="w-3.5 h-3.5 text-blox-muted" />
                      <span className="text-xs text-blox-muted">Utilization</span>
                    </div>
                    <span className="text-sm font-semibold text-blox-text tabular-nums">{metrics.gpu_util_percent.toFixed(0)}%</span>
                  </div>
                  <ProgressBar value={metrics.gpu_util_percent} size="md" />
                </div>

                {/* VRAM */}
                {metrics.gpu_vram_total_bytes > 0 && (
                  <div>
                    <div className="flex items-center justify-between mb-1.5">
                      <div className="flex items-center gap-2">
                        <MemoryStick className="w-3.5 h-3.5 text-blox-muted" />
                        <span className="text-xs text-blox-muted">VRAM</span>
                      </div>
                      <span className="text-xs text-blox-muted tabular-nums">{formatBytes(metrics.gpu_vram_used_bytes)} / {formatBytes(metrics.gpu_vram_total_bytes)}</span>
                    </div>
                    <ProgressBar value={gpuVramPct} size="md" label={`${gpuVramPct.toFixed(0)}%`} />
                  </div>
                )}

                {/* Power placeholder */}
                <div className="flex items-center justify-between py-2 border-t border-blox-border">
                  <div className="flex items-center gap-2">
                    <Zap className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">Power Draw</span>
                  </div>
                  <span className="text-[10px] text-blox-muted">Phase 2</span>
                </div>
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center py-8 text-blox-muted">
                <Zap className="w-8 h-8 mb-2 opacity-20" />
                <p className="text-xs">No GPU metrics available</p>
                <p className="text-[10px] mt-1">go-nvml integration in Phase 2</p>
              </div>
            )}
          </div>
        </div>

        {/* Services + Containers panels */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          <ServicePanel services={services} machineId={id} hubUrl={HUB_URL} />
          <ContainerPanel containers={containers} machineId={id} hubUrl={HUB_URL} />
        </div>

        {/* Terminal */}
        <div className="bg-blox-card border border-blox-border rounded-lg p-5">
          <div className="flex items-center gap-2 mb-4">
            <Terminal className="w-4 h-4 text-blox-muted" />
            <h3 className="text-sm font-semibold text-blox-text">Terminal</h3>
          </div>
          <div className="flex flex-col items-center justify-center py-12 bg-black/30 rounded-md border border-blox-border">
            <Lock className="w-10 h-10 text-blox-muted mb-3 opacity-20" />
            <p className="text-sm text-blox-muted">Remote Terminal</p>
            <p className="text-xs text-blox-muted mt-1">Coming in Phase 3 - xterm.js + pty</p>
          </div>
        </div>
      </main>
    </div>
  );
}
