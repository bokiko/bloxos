"use client";

import { useEffect, useState, useCallback, useRef, use } from "react";
import Link from "next/link";
import dynamic from "next/dynamic";
import { useSSE } from "@/contexts/SSEContext";
import { ProgressBar } from "@/components/ProgressBar";
import { StatusBadge, MachineStatus } from "@/components/StatusBadge";
import { ServicePanel, Service } from "@/components/ServicePanel";
import { ContainerPanel, Container } from "@/components/ContainerPanel";
import { RebootModal } from "@/components/RebootModal";
import { MetricCharts } from "@/components/MetricCharts";
import {
  ArrowLeft, Cpu, HardDrive, MemoryStick, Thermometer,
  Activity, Zap, Monitor, Terminal as TerminalIcon, RotateCcw,
  Lock, Unlock, X, Maximize2, Minimize2, Wifi, BarChart3,
} from "lucide-react";

const TerminalComponent = dynamic(
  () => import("@/components/Terminal").then((m) => ({ default: m.Terminal })),
  { ssr: false }
);

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";
const HUB_WS_URL = HUB_URL.replace(/^http/, "ws");

function authHeaders(): Record<string, string> {
  const token = typeof window !== "undefined" ? localStorage.getItem("bloxos_token") : null;
  return token ? { Authorization: `Bearer ${token}` } : {};
}

interface GPUData {
  index: number;
  name: string;
  temp_c: number;
  util_percent: number;
  mem_used_bytes: number;
  mem_total_bytes: number;
  power_watts: number;
  fan_percent: number;
}

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
  gpus: GPUData[];
  latency_ms: number;
}

type TerminalState = "locked" | "pin_entry" | "connecting" | "active" | "disconnected";

function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes === 0) return "0";
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
  if (data.gpus && data.gpus.length > 0) {
    for (const g of data.gpus) {
      if ((g.temp_c ?? 0) > 80) return "warning";
    }
  } else if ((data.metrics?.gpu_temp ?? 0) > 80) {
    return "warning";
  }
  const diskPct = (data.metrics?.disk_total_bytes ?? 0) > 0
    ? ((data.metrics?.disk_used_bytes ?? 0) / data.metrics.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return "warning";
  return "online";
}

export default function MachineDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { getMachine, connected } = useSSE();
  const [data, setData] = useState<MachineData | null>(null);
  const [services, setServices] = useState<Service[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [showReboot, setShowReboot] = useState(false);
  const [showCharts, setShowCharts] = useState(true);

  const [termState, setTermState] = useState<TerminalState>("locked");
  const [termSessionId, setTermSessionId] = useState<string | null>(null);
  const [pinInput, setPinInput] = useState("");
  const [pinError, setPinError] = useState(false);
  const [termExpanded, setTermExpanded] = useState(false);
  const pinInputRef = useRef<HTMLInputElement>(null);

  const fetchData = useCallback(async () => {
    try {
      const hdrs = authHeaders();
      const [machineRes, servicesRes, containersRes] = await Promise.all([
        fetch(`${HUB_URL}/api/machines/${id}`, { headers: hdrs }),
        fetch(`${HUB_URL}/api/machines/${id}/services`, { headers: hdrs }),
        fetch(`${HUB_URL}/api/machines/${id}/containers`, { headers: hdrs }),
      ]);

      if (!machineRes.ok) {
        setError(machineRes.status === 404 ? "Machine not found" : "Failed to load");
        return;
      }

      const machineJson = await machineRes.json();
      setData(machineJson);
      setLastUpdated(new Date().toISOString());
      setError(null);

      if (servicesRes.ok) setServices(await servicesRes.json());
      if (containersRes.ok) setContainers(await containersRes.json());
    } catch {
      setError("Cannot reach hub");
    }
  }, [id]);

  // Initial REST fetch for full machine data (gpus, services, containers)
  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // Update metrics from the global SSE stream
  useEffect(() => {
    const sseData = getMachine(id);
    if (!sseData) return;

    setData((prev) => {
      if (!prev) return prev;
      return {
        ...prev,
        metrics: {
          cpu_percent: sseData.cpu_percent ?? prev.metrics.cpu_percent,
          ram_used_bytes: sseData.ram_used_bytes ?? prev.metrics.ram_used_bytes,
          ram_total_bytes: sseData.ram_total_bytes ?? prev.metrics.ram_total_bytes,
          disk_used_bytes: sseData.disk_used_bytes ?? prev.metrics.disk_used_bytes,
          disk_total_bytes: sseData.disk_total_bytes ?? prev.metrics.disk_total_bytes,
          gpu_temp: sseData.gpu_temp ?? prev.metrics.gpu_temp,
          gpu_util_percent: sseData.gpu_util_percent ?? prev.metrics.gpu_util_percent,
          gpu_vram_used_bytes: sseData.gpu_vram_used_bytes ?? prev.metrics.gpu_vram_used_bytes,
          gpu_vram_total_bytes: sseData.gpu_vram_total_bytes ?? prev.metrics.gpu_vram_total_bytes,
        },
        machine: { ...prev.machine, status: "online" },
        latency_ms: sseData.latency_ms ?? prev.latency_ms,
      };
    });
    setLastUpdated(new Date().toISOString());

    // Update services/containers if SSE carried them
    const raw = sseData as unknown as Record<string, unknown>;
    if (raw._services) setServices(raw._services as Service[]);
    if (raw._containers) setContainers(raw._containers as Container[]);
  }, [getMachine, id]);

  useEffect(() => {
    const interval = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(interval);
  }, []);
  void tick;

  const handlePinSubmit = useCallback(() => {
    setPinError(false);
    setTermState("connecting");
    const hdrs = authHeaders();
    hdrs["Content-Type"] = "application/json";
    fetch(`${HUB_URL}/api/machines/${id}/terminal`, {
      method: "POST",
      headers: hdrs,
      body: JSON.stringify({ pin: pinInput }),
    })
      .then((res) => {
        if (res.status === 403) {
          setPinError(true);
          setPinInput("");
          setTermState("pin_entry");
          pinInputRef.current?.focus();
          return null;
        }
        if (!res.ok) throw new Error("Failed to start terminal");
        return res.json();
      })
      .then((d) => {
        if (!d) return;
        setPinInput("");
        setTermSessionId(d.session_id);
        setTermState("active");
      })
      .catch(() => {
        setTermState("disconnected");
      });
  }, [pinInput, id]);

  const handleTerminalClose = useCallback(() => {
    if (termSessionId) {
      fetch(`${HUB_URL}/api/machines/${id}/terminal/${termSessionId}`, {
        method: "DELETE",
        headers: authHeaders(),
      }).catch(() => {});
    }
    setTermSessionId(null);
    setTermState("locked");
    setTermExpanded(false);
  }, [termSessionId, id]);

  const handleTerminalDisconnect = useCallback(() => {
    setTermState("disconnected");
  }, []);

  useEffect(() => {
    if (termState === "pin_entry") {
      setTimeout(() => pinInputRef.current?.focus(), 100);
    }
  }, [termState]);

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
  const ramPct = (metrics?.ram_total_bytes ?? 0) > 0 ? ((metrics?.ram_used_bytes ?? 0) / metrics.ram_total_bytes) * 100 : 0;
  const diskPct = (metrics?.disk_total_bytes ?? 0) > 0 ? ((metrics?.disk_used_bytes ?? 0) / metrics.disk_total_bytes) * 100 : 0;
  const gpus = data.gpus || [];
  const hasGpu = gpus.length > 0;
  const isOnline = machine.status !== "offline";

  return (
    <div className="min-h-screen bg-blox-bg">
      {showReboot && (
        <RebootModal
          hostname={machine.hostname}
          machineId={id}
          hubUrl={HUB_URL}
          onClose={() => setShowReboot(false)}
        />
      )}

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
            {connected && (
              <span className="flex items-center gap-1 text-[10px] text-blox-green mr-1">
                <Wifi className="w-3 h-3" />
                Live
              </span>
            )}
            {(data.latency_ms ?? 0) > 0 && (
              <span className="flex items-center gap-1 text-[10px] text-blox-muted mr-2">
                <Wifi className="w-3 h-3" />
                {data.latency_ms}ms
              </span>
            )}
            {lastUpdated && (
              <span className="text-[10px] text-blox-muted mr-2">
                updated: {timeSince(lastUpdated)}
              </span>
            )}
            <button
              onClick={() => setShowReboot(true)}
              disabled={!isOnline}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border transition-colors ${
                !isOnline
                  ? "border-blox-border text-blox-muted opacity-50 cursor-not-allowed"
                  : "border-blox-border text-blox-text hover:border-blox-red/40 hover:text-blox-red hover:bg-blox-red/5"
              }`}
            >
              <RotateCcw className="w-3.5 h-3.5" />
              Reboot
            </button>
            <button
              onClick={() => {
                if (termState === "locked") setTermState("pin_entry");
                else if (termState === "active" || termState === "connecting") handleTerminalClose();
              }}
              disabled={!isOnline}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs border transition-colors ${
                !isOnline
                  ? "border-blox-border text-blox-muted opacity-50 cursor-not-allowed"
                  : termState === "active"
                    ? "border-blox-green/40 text-blox-green hover:bg-blox-green/10"
                    : "border-blox-border text-blox-text hover:border-blox-blue/40 hover:text-blox-blue hover:bg-blox-blue/5"
              }`}
            >
              <TerminalIcon className="w-3.5 h-3.5" />
              {termState === "active" ? "Terminal Active" : "Terminal"}
            </button>
          </div>
        </div>
      </header>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">
        <div className="flex flex-wrap gap-4 text-xs text-blox-muted">
          {machine.ip && <span>IP: <span className="text-blox-text">{machine.ip}</span></span>}
          {machine.os && <span>OS: <span className="text-blox-text">{machine.os}</span></span>}
          <span>ID: <span className="text-blox-text font-mono text-[10px]">{machine.id}</span></span>
          {(data.latency_ms ?? 0) > 0 && (
            <span>Latency: <span className="text-blox-text">{data.latency_ms}ms</span></span>
          )}
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          <div className="bg-blox-card border border-blox-border rounded-lg p-5">
            <div className="flex items-center gap-2 mb-5">
              <Activity className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">System</h3>
            </div>
            <div className="space-y-4">
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <Cpu className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">CPU</span>
                  </div>
                  <span className="text-sm font-semibold text-blox-text tabular-nums">{(metrics?.cpu_percent ?? 0).toFixed(1)}%</span>
                </div>
                <ProgressBar value={metrics?.cpu_percent ?? 0} size="md" />
              </div>
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <MemoryStick className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">RAM</span>
                  </div>
                  <span className="text-xs text-blox-muted tabular-nums">{formatBytes(metrics?.ram_used_bytes)} / {formatBytes(metrics?.ram_total_bytes)}</span>
                </div>
                <ProgressBar value={ramPct} size="md" label={`${ramPct.toFixed(0)}%`} />
              </div>
              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <div className="flex items-center gap-2">
                    <HardDrive className="w-3.5 h-3.5 text-blox-muted" />
                    <span className="text-xs text-blox-muted">Disk</span>
                  </div>
                  <span className="text-xs text-blox-muted tabular-nums">{formatBytes(metrics?.disk_used_bytes)} / {formatBytes(metrics?.disk_total_bytes)}</span>
                </div>
                <ProgressBar value={diskPct} size="md" label={`${diskPct.toFixed(0)}%`} />
              </div>
              <div className="flex items-center justify-between py-2 border-t border-blox-border">
                <div className="flex items-center gap-2">
                  <Wifi className="w-3.5 h-3.5 text-blox-muted" />
                  <span className="text-xs text-blox-muted">Network Latency</span>
                </div>
                <span className="text-xs text-blox-text tabular-nums">
                  {(data.latency_ms ?? 0) > 0 ? `${data.latency_ms}ms` : "N/A"}
                </span>
              </div>
            </div>
          </div>

          <div className="bg-blox-card border border-blox-border rounded-lg p-5">
            <div className="flex items-center gap-2 mb-5">
              <Zap className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">GPU</h3>
              {!hasGpu && <span className="text-[10px] text-blox-muted">(no GPU detected)</span>}
            </div>
            {hasGpu ? (
              <div className="space-y-5">
                {gpus.map((gpu) => {
                  const vramPct = (gpu.mem_total_bytes ?? 0) > 0 ? ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100 : 0;
                  return (
                    <div key={gpu.index} className="space-y-3">
                      {gpus.length > 1 && (
                        <div className="text-[10px] text-blox-muted font-mono border-b border-blox-border pb-1">
                          GPU {gpu.index}: {gpu.name}
                        </div>
                      )}
                      {gpus.length === 1 && gpu.name && (
                        <div className="text-[10px] text-blox-muted font-mono mb-1">{gpu.name}</div>
                      )}
                      <div>
                        <div className="flex items-center justify-between mb-1.5">
                          <div className="flex items-center gap-2">
                            <Thermometer className="w-3.5 h-3.5 text-blox-muted" />
                            <span className="text-xs text-blox-muted">Temperature</span>
                          </div>
                          <span className={`text-sm font-semibold tabular-nums ${
                            (gpu.temp_c ?? 0) > 80 ? "text-blox-red" :
                            (gpu.temp_c ?? 0) > 70 ? "text-blox-amber" : "text-blox-green"
                          }`}>{gpu.temp_c ?? 0}&deg;C</span>
                        </div>
                        <ProgressBar value={gpu.temp_c ?? 0} size="md" />
                      </div>
                      <div>
                        <div className="flex items-center justify-between mb-1.5">
                          <div className="flex items-center gap-2">
                            <Cpu className="w-3.5 h-3.5 text-blox-muted" />
                            <span className="text-xs text-blox-muted">Utilization</span>
                          </div>
                          <span className="text-sm font-semibold text-blox-text tabular-nums">{(gpu.util_percent ?? 0).toFixed(0)}%</span>
                        </div>
                        <ProgressBar value={gpu.util_percent ?? 0} size="md" />
                      </div>
                      {(gpu.mem_total_bytes ?? 0) > 0 && (
                        <div>
                          <div className="flex items-center justify-between mb-1.5">
                            <div className="flex items-center gap-2">
                              <MemoryStick className="w-3.5 h-3.5 text-blox-muted" />
                              <span className="text-xs text-blox-muted">VRAM</span>
                            </div>
                            <span className="text-xs text-blox-muted tabular-nums">{formatBytes(gpu.mem_used_bytes)} / {formatBytes(gpu.mem_total_bytes)}</span>
                          </div>
                          <ProgressBar value={vramPct} size="md" label={`${vramPct.toFixed(0)}%`} />
                        </div>
                      )}
                      <div className="flex items-center justify-between py-2 border-t border-blox-border">
                        <div className="flex items-center gap-2">
                          <Zap className="w-3.5 h-3.5 text-blox-muted" />
                          <span className="text-xs text-blox-muted">Power</span>
                        </div>
                        <span className="text-xs text-blox-text tabular-nums">
                          {(gpu.power_watts ?? 0) > 0 ? `${(gpu.power_watts ?? 0).toFixed(0)} W` : "N/A"}
                        </span>
                      </div>
                      <div className="flex items-center justify-between py-1">
                        <div className="flex items-center gap-2">
                          <Activity className="w-3.5 h-3.5 text-blox-muted" />
                          <span className="text-xs text-blox-muted">Fan</span>
                        </div>
                        <span className="text-xs text-blox-text tabular-nums">
                          {(gpu.fan_percent ?? 0) > 0 ? `${(gpu.fan_percent ?? 0).toFixed(0)}%` : "N/A"}
                        </span>
                      </div>
                    </div>
                  );
                })}
              </div>
            ) : (
              <div className="flex flex-col items-center justify-center py-8 text-blox-muted">
                <Zap className="w-8 h-8 mb-2 opacity-20" />
                <p className="text-xs">No GPU metrics available</p>
                <p className="text-[10px] mt-1">nvidia-smi not detected on this machine</p>
              </div>
            )}
          </div>
        </div>

        <div className="bg-blox-card border border-blox-border rounded-lg p-5">
          <div className="flex items-center justify-between mb-4">
            <div className="flex items-center gap-2">
              <BarChart3 className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">Metric History</h3>
            </div>
            <button
              onClick={() => setShowCharts(!showCharts)}
              className="text-xs text-blox-muted hover:text-blox-text transition-colors"
            >
              {showCharts ? "Hide" : "Show"}
            </button>
          </div>
          {showCharts && <MetricCharts machineId={id} hasGpu={hasGpu} />}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          <ServicePanel services={services} machineId={id} hubUrl={HUB_URL} />
          <ContainerPanel containers={containers} machineId={id} hubUrl={HUB_URL} />
        </div>

        <div className="bg-blox-card border border-blox-border rounded-lg overflow-hidden">
          <div className="flex items-center justify-between px-5 py-3 border-b border-blox-border">
            <div className="flex items-center gap-2">
              <TerminalIcon className="w-4 h-4 text-blox-blue" />
              <h3 className="text-sm font-semibold text-blox-text">Terminal</h3>
              {termState === "active" && <span className="text-[10px] px-1.5 py-0.5 rounded bg-blox-green/10 text-blox-green border border-blox-green/20">connected</span>}
              {termState === "connecting" && <span className="text-[10px] px-1.5 py-0.5 rounded bg-blox-blue/10 text-blox-blue border border-blox-blue/20">connecting...</span>}
              {termState === "disconnected" && <span className="text-[10px] px-1.5 py-0.5 rounded bg-blox-red/10 text-blox-red border border-blox-red/20">disconnected</span>}
            </div>
            <div className="flex items-center gap-1">
              {termState === "active" && (
                <>
                  <button onClick={() => setTermExpanded(!termExpanded)} className="p-1.5 rounded hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors" title={termExpanded ? "Collapse" : "Expand"}>
                    {termExpanded ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
                  </button>
                  <button onClick={handleTerminalClose} className="p-1.5 rounded hover:bg-blox-red/10 text-blox-muted hover:text-blox-red transition-colors" title="Close terminal">
                    <X className="w-3.5 h-3.5" />
                  </button>
                </>
              )}
              {termState === "disconnected" && (
                <button onClick={() => setTermState("pin_entry")} className="text-xs text-blox-blue hover:text-blox-blue/80 transition-colors">Reconnect</button>
              )}
            </div>
          </div>

          {termState === "locked" && (
            <div className="flex flex-col items-center justify-center py-12 bg-black/30">
              <Lock className="w-10 h-10 text-blox-muted mb-3 opacity-20" />
              <p className="text-sm text-blox-muted">Remote Terminal</p>
              <p className="text-xs text-blox-muted mt-1 mb-4">Enter PIN to unlock terminal access</p>
              <button onClick={() => isOnline && setTermState("pin_entry")} disabled={!isOnline} className={`flex items-center gap-2 px-4 py-2 rounded-md text-xs font-medium transition-colors ${isOnline ? "bg-blox-blue/10 text-blox-blue border border-blox-blue/30 hover:bg-blox-blue/20" : "bg-blox-border text-blox-muted cursor-not-allowed border border-blox-border"}`}>
                <Unlock className="w-3.5 h-3.5" />Unlock Terminal
              </button>
            </div>
          )}

          {termState === "pin_entry" && (
            <div className="flex flex-col items-center justify-center py-12 bg-black/30">
              <Lock className="w-8 h-8 text-blox-blue mb-3 opacity-50" />
              <p className="text-sm text-blox-text mb-4">Enter PIN to open terminal</p>
              <form onSubmit={(e) => { e.preventDefault(); handlePinSubmit(); }} className="flex items-center gap-2">
                <input ref={pinInputRef} type="password" maxLength={8} value={pinInput} onChange={(e) => { setPinInput(e.target.value); setPinError(false); }} placeholder="PIN" className={`w-32 px-3 py-2 text-sm text-center font-mono rounded-md bg-blox-bg border ${pinError ? "border-blox-red" : "border-blox-border"} text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue`} autoComplete="off" />
                <button type="submit" className="px-4 py-2 text-xs font-medium rounded-md bg-blox-blue/10 text-blox-blue border border-blox-blue/30 hover:bg-blox-blue/20 transition-colors">Open</button>
                <button type="button" onClick={() => { setTermState("locked"); setPinInput(""); setPinError(false); }} className="px-3 py-2 text-xs text-blox-muted hover:text-blox-text transition-colors">Cancel</button>
              </form>
              {pinError && <p className="text-xs text-blox-red mt-2">Invalid PIN</p>}
            </div>
          )}

          {termState === "connecting" && (
            <div className="flex flex-col items-center justify-center py-12 bg-black/30">
              <div className="w-6 h-6 border-2 border-blox-blue border-t-transparent rounded-full animate-spin mb-3" />
              <p className="text-sm text-blox-muted">Starting terminal session...</p>
            </div>
          )}

          {termState === "active" && termSessionId && (
            <div className="bg-[#0a0a0f] transition-all duration-200" style={{ height: termExpanded ? "600px" : "360px" }}>
              <TerminalComponent sessionId={termSessionId} hubWsUrl={HUB_WS_URL} onDisconnect={handleTerminalDisconnect} />
            </div>
          )}

          {termState === "disconnected" && (
            <div className="flex flex-col items-center justify-center py-12 bg-black/30">
              <TerminalIcon className="w-8 h-8 text-blox-red mb-3 opacity-30" />
              <p className="text-sm text-blox-muted">Terminal disconnected</p>
              <div className="flex items-center gap-2 mt-4">
                <button onClick={() => setTermState("pin_entry")} className="px-4 py-2 text-xs font-medium rounded-md bg-blox-blue/10 text-blox-blue border border-blox-blue/30 hover:bg-blox-blue/20 transition-colors">Reconnect</button>
                <button onClick={() => { setTermState("locked"); setTermSessionId(null); }} className="px-3 py-2 text-xs text-blox-muted hover:text-blox-text transition-colors">Close</button>
              </div>
            </div>
          )}
        </div>
      </main>
    </div>
  );
}
