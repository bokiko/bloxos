"use client";

import { useEffect, useState, useCallback, useRef, use, useMemo } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import dynamic from "next/dynamic";
import { useSSE } from "@/contexts/SSEContext";
import { ProgressBar } from "@/components/ProgressBar";
import { StatusBadge, MachineStatus } from "@/components/StatusBadge";
import { ServicePanel, Service } from "@/components/ServicePanel";
import { ContainerPanel, Container } from "@/components/ContainerPanel";
import { RebootModal } from "@/components/RebootModal";
import { MetricCharts } from "@/components/MetricCharts";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { motion } from "framer-motion";
import {
  ArrowLeft, Cpu, HardDrive, MemoryStick, Thermometer,
  Activity, Zap, Monitor, Terminal as TerminalIcon, RotateCcw,
  Lock, Unlock, X, Maximize2, Minimize2, Wifi, BarChart3, Trash2,
  LayoutDashboard, Box, Container as ContainerIcon,
} from "lucide-react";
import { HUB_URL, getAuthHeaders } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";

type DetailTab = "overview" | "services" | "containers" | "metrics" | "terminal";
const DETAIL_TABS: readonly DetailTab[] = ["overview", "services", "containers", "metrics", "terminal"] as const;
function parseTab(raw: string | null): DetailTab {
  return DETAIL_TABS.includes(raw as DetailTab) ? (raw as DetailTab) : "overview";
}

const TerminalComponent = dynamic(
  () => import("@/components/Terminal").then((m) => ({ default: m.Terminal })),
  { ssr: false }
);

function authHeaders(): Record<string, string> {
  return getAuthHeaders();
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

interface HardwareDiskInfo {
  device: string;
  model?: string;
  size_bytes?: number;
  type?: string;
}

interface HardwareNetworkInfo {
  name: string;
  mac?: string;
  ipv4?: string;
  speed_mbps?: number;
}

interface HardwareInfo {
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
  disks?: HardwareDiskInfo[];
  network_interfaces?: HardwareNetworkInfo[];
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
  hardware_info?: HardwareInfo | null;
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
  const [baseData, setBaseData] = useState<MachineData | null>(null);
  const [services, setServices] = useState<Service[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [showReboot, setShowReboot] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const { hasScope } = useAuth();
  const canDelete = hasScope("fleet.admin");
  const canControl = hasScope("fleet.control");
  const router = useRouter();
  const searchParams = useSearchParams();
  const activeTab = parseTab(searchParams.get("tab"));
  const setActiveTab = useCallback((next: DetailTab) => {
    const params = new URLSearchParams(searchParams.toString());
    if (next === "overview") {
      params.delete("tab");
    } else {
      params.set("tab", next);
    }
    const query = params.toString();
    router.replace(query ? `?${query}` : "?", { scroll: false });
  }, [router, searchParams]);

  const [termState, setTermState] = useState<TerminalState>("locked");
  const [termSessionId, setTermSessionId] = useState<string | null>(null);
  const [termBrowserToken, setTermBrowserToken] = useState<string | null>(null);
  const [pinInput, setPinInput] = useState("");
  const [pinError, setPinError] = useState(false);
  const [termExpanded, setTermExpanded] = useState(false);
  const pinInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const hdrs = authHeaders();
        const [machineRes, servicesRes, containersRes] = await Promise.all([
          fetch(`${HUB_URL}/api/machines/${id}`, { headers: hdrs }),
          fetch(`${HUB_URL}/api/machines/${id}/services`, { headers: hdrs }),
          fetch(`${HUB_URL}/api/machines/${id}/containers`, { headers: hdrs }),
        ]);

        if (!machineRes.ok) {
          if (!cancelled) {
            setError(machineRes.status === 404 ? "Machine not found" : "Failed to load");
          }
          return;
        }

        const machineJson = await machineRes.json();
        if (cancelled) return;

        setBaseData(machineJson);
        setLastUpdated(new Date().toISOString());
        setError(null);

        if (servicesRes.ok) setServices(await servicesRes.json());
        if (containersRes.ok) setContainers(await containersRes.json());
      } catch {
        if (!cancelled) {
          setError("Cannot reach hub");
        }
      }
    }

    void load();
    return () => {
      cancelled = true;
    };
  }, [id]);

  const sseData = getMachine(id);
  const data = useMemo<MachineData | null>(() => {
    if (!baseData || !sseData) return baseData;

    return {
      ...baseData,
      metrics: {
        cpu_percent: sseData.cpu_percent ?? baseData.metrics.cpu_percent,
        ram_used_bytes: sseData.ram_used_bytes ?? baseData.metrics.ram_used_bytes,
        ram_total_bytes: sseData.ram_total_bytes ?? baseData.metrics.ram_total_bytes,
        disk_used_bytes: sseData.disk_used_bytes ?? baseData.metrics.disk_used_bytes,
        disk_total_bytes: sseData.disk_total_bytes ?? baseData.metrics.disk_total_bytes,
        gpu_temp: sseData.gpu_temp ?? baseData.metrics.gpu_temp,
        gpu_util_percent: sseData.gpu_util_percent ?? baseData.metrics.gpu_util_percent,
        gpu_vram_used_bytes: sseData.gpu_vram_used_bytes ?? baseData.metrics.gpu_vram_used_bytes,
        gpu_vram_total_bytes: sseData.gpu_vram_total_bytes ?? baseData.metrics.gpu_vram_total_bytes,
      },
      machine: { ...baseData.machine, status: "online" },
      latency_ms: sseData.latency_ms ?? baseData.latency_ms,
    };
  }, [baseData, sseData]);

  const effectiveLastUpdated = useMemo(
    () => (sseData ? new Date().toISOString() : lastUpdated),
    [sseData, lastUpdated]
  );
  const liveServices = useMemo(() => {
    const raw = sseData as Record<string, unknown> | undefined;
    return Array.isArray(raw?._services) ? (raw._services as Service[]) : services;
  }, [sseData, services]);
  const liveContainers = useMemo(() => {
    const raw = sseData as Record<string, unknown> | undefined;
    return Array.isArray(raw?._containers) ? (raw._containers as Container[]) : containers;
  }, [sseData, containers]);

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
        setTermBrowserToken(d.browser_token);
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
    setTermBrowserToken(null);
    setTermState("locked");
    setTermExpanded(false);
  }, [termSessionId, id]);

  const handleTerminalDisconnect = useCallback(() => {
    setTermBrowserToken(null);
    setTermState("disconnected");
  }, []);

  const handleDeleteMachine = useCallback(async () => {
    setDeleting(true);
    try {
      const res = await fetch(`${HUB_URL}/api/machines/${id}`, {
        method: "DELETE",
        headers: authHeaders(),
      });
      if (res.ok) {
        router.push("/");
      } else {
        setDeleting(false);
        setShowDeleteConfirm(false);
      }
    } catch {
      setDeleting(false);
      setShowDeleteConfirm(false);
    }
  }, [id, router]);

  useEffect(() => {
    if (termState === "pin_entry") {
      setTimeout(() => pinInputRef.current?.focus(), 100);
    }
  }, [termState]);

  if (error && !data) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="text-center">
          <p className="text-red-400 text-sm mb-4">{error}</p>
          <Link href="/" className="text-blox-blue text-sm hover:underline">Back to Fleet</Link>
        </div>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="flex items-center gap-2 text-blox-muted text-sm">
          <div className="w-4 h-4 border-2 border-blox-blue border-t-transparent rounded-full animate-spin" />
          Loading...
        </div>
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
  const sseM = getMachine(id);
  const machineTags = sseM?.tags ? sseM.tags.split(",").map((t: string) => t.trim().toLowerCase()) : [];
  const isAPIMachine = machineTags.includes("synology") || machineTags.includes("proxmox");

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.3 }}
      className="min-h-screen bg-blox-bg"
    >
      {/* Delete dialog */}
      <Dialog open={showDeleteConfirm} onOpenChange={(o) => { if (!o) setShowDeleteConfirm(false); }}>
        <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-md" showCloseButton={false}>
          <DialogHeader>
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-red-500/10">
                <Trash2 className="w-5 h-5 text-red-400" />
              </div>
              <DialogTitle className="text-blox-text">Delete Machine</DialogTitle>
            </div>
            <DialogDescription className="text-blox-muted text-xs mt-2">
              Are you sure you want to remove <span className="text-blox-text font-medium">{machine.hostname}</span> from BloxOS? This will delete all historical data.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="bg-transparent border-t-blox-border">
            <Button variant="outline" size="sm" onClick={() => setShowDeleteConfirm(false)} disabled={deleting} className="text-xs text-blox-muted border-blox-border">
              Cancel
            </Button>
            <Button variant="destructive" size="sm" onClick={handleDeleteMachine} disabled={deleting} className="text-xs">
              {deleting ? "Deleting..." : "Delete Machine"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {showReboot && (
        <RebootModal
          hostname={machine.hostname}
          machineId={id}
          hubUrl={HUB_URL}
          onClose={() => setShowReboot(false)}
        />
      )}

      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link href="/" className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-sm">
              <ArrowLeft className="w-4 h-4" />
              Fleet
            </Link>
            <span className="text-blox-border/50">/</span>
            <div className="flex items-center gap-2.5">
              <div className="p-1.5 rounded-lg bg-blox-border/50">
                <Monitor className="w-3.5 h-3.5 text-blox-muted" />
              </div>
              <h1 className="font-semibold text-blox-text tracking-tight">{machine.hostname}</h1>
              <StatusBadge status={status} />
            </div>
          </div>
          <div className="flex items-center gap-2">
            {connected && (
              <span className="flex items-center gap-1 text-[10px] text-emerald-400 mr-1">
                <Wifi className="w-3 h-3" />
                Live
              </span>
            )}
            {(data.latency_ms ?? 0) > 0 && (
              <span className="flex items-center gap-1 text-[10px] text-blox-muted font-mono tabular-nums mr-2">
                <Wifi className="w-3 h-3" />
                {data.latency_ms}ms
              </span>
            )}
            {effectiveLastUpdated && (
              <span className="text-[10px] text-blox-muted font-mono tabular-nums mr-2">
                {timeSince(effectiveLastUpdated)}
              </span>
            )}
            {canDelete && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setShowDeleteConfirm(true)}
                className="text-xs border-blox-border text-blox-muted hover:text-red-400 hover:border-red-500/30 gap-1.5"
              >
                <Trash2 className="w-3.5 h-3.5" />
                Delete
              </Button>
            )}
            {!isAPIMachine && canControl && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setShowReboot(true)}
                disabled={!isOnline}
                className="text-xs border-blox-border text-blox-text gap-1.5"
              >
                <RotateCcw className="w-3.5 h-3.5" />
                Reboot
              </Button>
            )}
          </div>
        </div>
      </header>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">
        {/* Info bar */}
        <div className="flex flex-wrap gap-4 text-xs text-blox-muted">
          {machine.ip && <span>IP: <span className="text-blox-text font-mono tabular-nums">{machine.ip}</span></span>}
          {machine.os && <span>OS: <span className="text-blox-text">{machine.os}</span></span>}
          <span>ID: <span className="text-blox-text font-mono text-[10px] tabular-nums">{machine.id}</span></span>
          {(data.latency_ms ?? 0) > 0 && (
            <span>Latency: <span className="text-blox-text font-mono tabular-nums">{data.latency_ms}ms</span></span>
          )}
        </div>

        <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as DetailTab)}>
          <TabsList variant="line" className="gap-1">
            <TabsTrigger value="overview" className="px-4 py-1.5 gap-1.5 text-sm">
              <LayoutDashboard className="w-4 h-4" />
              Overview
            </TabsTrigger>
            <TabsTrigger value="services" className="px-4 py-1.5 gap-1.5 text-sm">
              <Box className="w-4 h-4" />
              Services
            </TabsTrigger>
            <TabsTrigger value="containers" className="px-4 py-1.5 gap-1.5 text-sm">
              <ContainerIcon className="w-4 h-4" />
              Containers
            </TabsTrigger>
            <TabsTrigger value="metrics" className="px-4 py-1.5 gap-1.5 text-sm">
              <BarChart3 className="w-4 h-4" />
              Metrics
            </TabsTrigger>
            {!isAPIMachine && canControl && (
              <TabsTrigger value="terminal" className="px-4 py-1.5 gap-1.5 text-sm">
                <TerminalIcon className="w-4 h-4" />
                Terminal
                {termState === "active" && (
                  <span className="ml-1 inline-block w-1.5 h-1.5 rounded-full bg-emerald-400" />
                )}
              </TabsTrigger>
            )}
          </TabsList>

          {/* Overview — system panel, plus GPU only when a GPU is present */}
          <TabsContent value="overview" className="mt-6">
            <div className={`grid grid-cols-1 gap-6 ${hasGpu ? "lg:grid-cols-2" : ""}`}>
              <motion.div
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.05 }}
                className="bg-blox-card border border-blox-border rounded-xl p-5"
              >
                <div className="flex items-center gap-2.5 mb-5">
                  <div className="p-1.5 rounded-lg bg-blox-blue/10">
                    <Activity className="w-3.5 h-3.5 text-blox-blue" />
                  </div>
                  <h3 className="text-sm font-semibold text-blox-text">System</h3>
                </div>
                <div className="space-y-4">
                  <div>
                    <div className="flex items-center justify-between mb-1.5">
                      <div className="flex items-center gap-2">
                        <Cpu className="w-3.5 h-3.5 text-blox-muted" />
                        <span className="text-xs text-blox-muted">CPU</span>
                      </div>
                      <span className="text-sm font-semibold text-blox-text tabular-nums font-mono">{(metrics?.cpu_percent ?? 0).toFixed(1)}%</span>
                    </div>
                    <ProgressBar value={metrics?.cpu_percent ?? 0} size="md" />
                  </div>
                  <div>
                    <div className="flex items-center justify-between mb-1.5">
                      <div className="flex items-center gap-2">
                        <MemoryStick className="w-3.5 h-3.5 text-blox-muted" />
                        <span className="text-xs text-blox-muted">RAM</span>
                      </div>
                      <span className="text-xs text-blox-muted tabular-nums font-mono">{formatBytes(metrics?.ram_used_bytes)} / {formatBytes(metrics?.ram_total_bytes)}</span>
                    </div>
                    <ProgressBar value={ramPct} size="md" label={`${ramPct.toFixed(0)}%`} />
                  </div>
                  <div>
                    <div className="flex items-center justify-between mb-1.5">
                      <div className="flex items-center gap-2">
                        <HardDrive className="w-3.5 h-3.5 text-blox-muted" />
                        <span className="text-xs text-blox-muted">Disk</span>
                      </div>
                      <span className="text-xs text-blox-muted tabular-nums font-mono">{formatBytes(metrics?.disk_used_bytes)} / {formatBytes(metrics?.disk_total_bytes)}</span>
                    </div>
                    <ProgressBar value={diskPct} size="md" label={`${diskPct.toFixed(0)}%`} />
                  </div>
                  {(data.latency_ms ?? 0) > 0 && (
                    <div className="flex items-center justify-between py-2.5 border-t border-blox-border/50">
                      <div className="flex items-center gap-2">
                        <Wifi className="w-3.5 h-3.5 text-blox-muted" />
                        <span className="text-xs text-blox-muted">Network Latency</span>
                      </div>
                      <span className="text-xs text-blox-text tabular-nums font-mono">
                        {data.latency_ms}ms
                      </span>
                    </div>
                  )}
                </div>
              </motion.div>

              {hasGpu && (
                <motion.div
                  initial={{ opacity: 0, y: 10 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ delay: 0.1 }}
                  className="bg-blox-card border border-blox-border rounded-xl p-5"
                >
                  <div className="flex items-center gap-2.5 mb-5">
                    <div className="p-1.5 rounded-lg bg-blox-blue/10">
                      <Zap className="w-3.5 h-3.5 text-blox-blue" />
                    </div>
                    <h3 className="text-sm font-semibold text-blox-text">GPU</h3>
                  </div>
                  <div className="space-y-5">
                    {gpus.map((gpu) => {
                      const vramPct = (gpu.mem_total_bytes ?? 0) > 0 ? ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100 : 0;
                      return (
                        <div key={gpu.index} className="space-y-3">
                          {gpus.length > 1 && (
                            <div className="text-[10px] text-blox-muted font-mono border-b border-blox-border/50 pb-1">
                              GPU {gpu.index}: {gpu.name}
                            </div>
                          )}
                          {gpus.length === 1 && gpu.name && (
                            <Badge variant="outline" className="text-[10px] border-blox-border text-blox-muted font-mono h-auto py-0 px-2 mb-1">
                              {gpu.name}
                            </Badge>
                          )}
                          <div>
                            <div className="flex items-center justify-between mb-1.5">
                              <div className="flex items-center gap-2">
                                <Thermometer className="w-3.5 h-3.5 text-blox-muted" />
                                <span className="text-xs text-blox-muted">Temperature</span>
                              </div>
                              <span className={`text-sm font-semibold tabular-nums font-mono ${
                                (gpu.temp_c ?? 0) > 80 ? "text-red-400" :
                                (gpu.temp_c ?? 0) > 60 ? "text-amber-400" : "text-emerald-400"
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
                              <span className="text-sm font-semibold text-blox-text tabular-nums font-mono">{(gpu.util_percent ?? 0).toFixed(0)}%</span>
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
                                <span className="text-xs text-blox-muted tabular-nums font-mono">{formatBytes(gpu.mem_used_bytes)} / {formatBytes(gpu.mem_total_bytes)}</span>
                              </div>
                              <ProgressBar value={vramPct} size="md" label={`${vramPct.toFixed(0)}%`} />
                            </div>
                          )}
                          {(gpu.power_watts ?? 0) > 0 && (
                            <div className="flex items-center justify-between py-2 border-t border-blox-border/50">
                              <div className="flex items-center gap-2">
                                <Zap className="w-3.5 h-3.5 text-blox-muted" />
                                <span className="text-xs text-blox-muted">Power</span>
                              </div>
                              <span className="text-xs text-blox-text tabular-nums font-mono">
                                {(gpu.power_watts ?? 0).toFixed(0)} W
                              </span>
                            </div>
                          )}
                          {(gpu.fan_percent ?? 0) > 0 && (
                            <div className="flex items-center justify-between py-1">
                              <div className="flex items-center gap-2">
                                <Activity className="w-3.5 h-3.5 text-blox-muted" />
                                <span className="text-xs text-blox-muted">Fan</span>
                              </div>
                              <span className="text-xs text-blox-text tabular-nums font-mono">
                                {(gpu.fan_percent ?? 0).toFixed(0)}%
                              </span>
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </motion.div>
              )}
            </div>

            {data.hardware_info && <HardwarePanel hw={data.hardware_info} />}
          </TabsContent>

          <TabsContent value="services" className="mt-6">
            <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
              <ServicePanel services={liveServices} machineId={id} hubUrl={HUB_URL} />
            </motion.div>
          </TabsContent>

          <TabsContent value="containers" className="mt-6">
            <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
              <ContainerPanel containers={liveContainers} machineId={id} hubUrl={HUB_URL} />
            </motion.div>
          </TabsContent>

          <TabsContent value="metrics" className="mt-6">
            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: 0.05 }}
              className="bg-blox-card border border-blox-border rounded-xl p-5"
            >
              <MetricCharts machineId={id} hasGpu={hasGpu} />
            </motion.div>
          </TabsContent>

          {/* Terminal tab — keepMounted so the PTY WebSocket survives tab switches. */}
          {!isAPIMachine && canControl && (
            <TabsContent value="terminal" keepMounted className="mt-6 data-[hidden]:hidden">
              <motion.div
                initial={{ opacity: 0, y: 10 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.05 }}
                className="bg-blox-card border border-blox-border rounded-xl overflow-hidden"
              >
                {/* Terminal header bar — macOS style */}
                <div className="flex items-center justify-between px-5 py-3 border-b border-blox-border/50 bg-blox-bg/30">
                  <div className="flex items-center gap-3">
                    <div className="flex items-center gap-1.5">
                      <span className="w-3 h-3 rounded-full bg-red-500/80" />
                      <span className="w-3 h-3 rounded-full bg-amber-500/80" />
                      <span className="w-3 h-3 rounded-full bg-emerald-500/80" />
                    </div>
                    <div className="flex items-center gap-2">
                      <TerminalIcon className="w-3.5 h-3.5 text-blox-muted" />
                      <h3 className="text-sm font-semibold text-blox-text">Terminal</h3>
                      {termState === "active" && (
                        <Badge variant="outline" className="text-[10px] border-emerald-500/30 bg-emerald-500/10 text-emerald-400 h-auto py-0 px-1.5">
                          connected
                        </Badge>
                      )}
                      {termState === "connecting" && (
                        <Badge variant="outline" className="text-[10px] border-blox-blue/30 bg-blox-blue/10 text-blox-blue h-auto py-0 px-1.5">
                          connecting...
                        </Badge>
                      )}
                      {termState === "disconnected" && (
                        <Badge variant="outline" className="text-[10px] border-red-500/30 bg-red-500/10 text-red-400 h-auto py-0 px-1.5">
                          disconnected
                        </Badge>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    {termState === "active" && (
                      <>
                        <Button variant="ghost" size="icon-xs" onClick={() => setTermExpanded(!termExpanded)} className="text-blox-muted hover:text-blox-text" title={termExpanded ? "Collapse" : "Expand"}>
                          {termExpanded ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
                        </Button>
                        <Button variant="ghost" size="icon-xs" onClick={handleTerminalClose} className="text-blox-muted hover:text-red-400" title="Close terminal">
                          <X className="w-3.5 h-3.5" />
                        </Button>
                      </>
                    )}
                    {termState === "disconnected" && (
                      <Button variant="ghost" size="xs" onClick={() => setTermState("pin_entry")} className="text-xs text-blox-blue">
                        Reconnect
                      </Button>
                    )}
                  </div>
                </div>

                {termState === "locked" && (
                  <div className="flex flex-col items-center justify-center py-12 bg-black/20">
                    <Lock className="w-10 h-10 text-blox-muted mb-3 opacity-20" />
                    <p className="text-sm text-blox-muted">Remote Terminal</p>
                    <p className="text-xs text-blox-muted/60 mt-1 mb-4">Enter PIN to unlock terminal access</p>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => isOnline && setTermState("pin_entry")}
                      disabled={!isOnline}
                      className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10 gap-2"
                    >
                      <Unlock className="w-3.5 h-3.5" />
                      Unlock Terminal
                    </Button>
                  </div>
                )}

                {termState === "pin_entry" && (
                  <div className="flex flex-col items-center justify-center py-12 bg-black/20">
                    <Lock className="w-8 h-8 text-blox-blue mb-3 opacity-50" />
                    <p className="text-sm text-blox-text mb-4">Enter PIN to open terminal</p>
                    <form onSubmit={(e) => { e.preventDefault(); handlePinSubmit(); }} className="flex items-center gap-2">
                      <Input
                        ref={pinInputRef}
                        type="password"
                        maxLength={8}
                        value={pinInput}
                        onChange={(e) => { setPinInput(e.target.value); setPinError(false); }}
                        placeholder="PIN"
                        className={`w-32 text-center font-mono bg-blox-bg border-blox-border text-blox-text h-9 text-sm ${pinError ? "border-red-500" : ""}`}
                        autoComplete="off"
                      />
                      <Button type="submit" variant="outline" size="sm" className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10">
                        Open
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => { setTermState("locked"); setPinInput(""); setPinError(false); }}
                        className="text-xs text-blox-muted"
                      >
                        Cancel
                      </Button>
                    </form>
                    {pinError && <p className="text-xs text-red-400 mt-2">Invalid PIN</p>}
                  </div>
                )}

                {termState === "connecting" && (
                  <div className="flex flex-col items-center justify-center py-12 bg-black/20">
                    <div className="w-6 h-6 border-2 border-blox-blue border-t-transparent rounded-full animate-spin mb-3" />
                    <p className="text-sm text-blox-muted">Starting terminal session...</p>
                  </div>
                )}

                {termState === "active" && termSessionId && (
                  <div className="bg-[#0a0a0f] transition-all duration-200" style={{ height: termExpanded ? "600px" : "360px" }}>
                    <TerminalComponent sessionId={termSessionId} browserToken={termBrowserToken ?? ""} onDisconnect={handleTerminalDisconnect} />
                  </div>
                )}

                {termState === "disconnected" && (
                  <div className="flex flex-col items-center justify-center py-12 bg-black/20">
                    <TerminalIcon className="w-8 h-8 text-red-400 mb-3 opacity-30" />
                    <p className="text-sm text-blox-muted">Terminal disconnected</p>
                    <div className="flex items-center gap-2 mt-4">
                      <Button variant="outline" size="sm" onClick={() => setTermState("pin_entry")} className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10">
                        Reconnect
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => { setTermState("locked"); setTermSessionId(null); }} className="text-xs text-blox-muted">
                        Close
                      </Button>
                    </div>
                  </div>
                )}
              </motion.div>
            </TabsContent>
          )}
        </Tabs>
      </main>
    </motion.div>
  );
}

function formatHardwareBytes(bytes: number | undefined | null): string {
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

function HardwareRow({ label, value }: { label: string; value: string | number | null | undefined }) {
  if (value === null || value === undefined || value === "" || value === 0) return null;
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 border-b border-blox-border/30 last:border-b-0">
      <span className="text-xs text-blox-muted">{label}</span>
      <span className="text-xs text-blox-text font-mono tabular-nums text-right truncate">{value}</span>
    </div>
  );
}

function HardwarePanel({ hw }: { hw: HardwareInfo }) {
  const diskLines = (hw.disks ?? []).filter((d) => (d.size_bytes ?? 0) > 0 || d.model);
  const nicLines = (hw.network_interfaces ?? []).filter((n) => n.name);
  const gpuNames = (hw.gpu_models ?? []).filter(Boolean);

  const uptime = formatUptime(hw.boot_time);
  const cpuDetail = [
    hw.cpu_cores ? `${hw.cpu_cores} cores` : "",
    hw.cpu_threads && hw.cpu_threads !== hw.cpu_cores ? `${hw.cpu_threads} threads` : "",
    hw.cpu_frequency_mhz && hw.cpu_frequency_mhz > 0 ? `${(hw.cpu_frequency_mhz / 1000).toFixed(1)} GHz` : "",
  ].filter(Boolean).join(" · ");

  return (
    <motion.div
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: 0.15 }}
      className="bg-blox-card border border-blox-border rounded-xl p-5 mt-6"
    >
      <div className="flex items-center gap-2.5 mb-5">
        <div className="p-1.5 rounded-lg bg-blox-blue/10">
          <Cpu className="w-3.5 h-3.5 text-blox-blue" />
        </div>
        <h3 className="text-sm font-semibold text-blox-text">Hardware</h3>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-x-8 gap-y-1">
        <div>
          <HardwareRow label="CPU" value={hw.cpu_model} />
          <HardwareRow label="" value={cpuDetail || undefined} />
          <HardwareRow label="Vendor" value={hw.cpu_vendor} />
          <HardwareRow label="Memory" value={formatHardwareBytes(hw.ram_total_bytes)} />
          <HardwareRow label="Architecture" value={hw.architecture} />
          <HardwareRow label="Kernel" value={hw.kernel_version} />
          <HardwareRow label="Platform" value={hw.platform_family} />
          <HardwareRow label="Virtualization" value={hw.virtualization} />
          <HardwareRow label="Uptime" value={uptime} />
        </div>

        <div>
          {gpuNames.length > 0 && (
            <>
              <div className="text-xs text-blox-muted mb-1.5 uppercase tracking-wider">GPU</div>
              {gpuNames.map((name) => (
                <div key={name} className="text-xs text-blox-text font-mono mb-1">{name}</div>
              ))}
            </>
          )}

          {diskLines.length > 0 && (
            <>
              <div className="text-xs text-blox-muted mt-3 mb-1.5 uppercase tracking-wider">Storage</div>
              {diskLines.map((d) => (
                <div key={d.device} className="flex items-baseline justify-between gap-4 py-1 border-b border-blox-border/30 last:border-b-0">
                  <span className="text-xs text-blox-muted font-mono">
                    {d.device.replace(/^\/dev\//, "")}
                    {d.type && <span className="ml-2 text-[10px] uppercase text-blox-muted/70">{d.type}</span>}
                  </span>
                  <span className="text-xs text-blox-text font-mono tabular-nums text-right truncate">
                    {formatHardwareBytes(d.size_bytes)}
                    {d.model && <span className="ml-2 text-blox-muted">{d.model}</span>}
                  </span>
                </div>
              ))}
            </>
          )}

          {nicLines.length > 0 && (
            <>
              <div className="text-xs text-blox-muted mt-3 mb-1.5 uppercase tracking-wider">Network</div>
              {nicLines.map((n) => (
                <div key={n.name} className="flex items-baseline justify-between gap-4 py-1 border-b border-blox-border/30 last:border-b-0">
                  <span className="text-xs text-blox-muted font-mono">{n.name}</span>
                  <span className="text-xs text-blox-text font-mono tabular-nums text-right truncate">
                    {n.ipv4}
                    {n.speed_mbps && n.speed_mbps > 0 && (
                      <span className="ml-2 text-blox-muted">{n.speed_mbps >= 1000 ? `${n.speed_mbps / 1000} Gb/s` : `${n.speed_mbps} Mb/s`}</span>
                    )}
                  </span>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
    </motion.div>
  );
}
