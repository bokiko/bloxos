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
import { HardwareCard, type HardwareInfo } from "@/components/HardwareCard";
import { MachineNotes } from "@/components/MachineNotes";
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
  Activity, Zap, Terminal as TerminalIcon, RotateCcw,
  Lock, Unlock, X, Maximize2, Minimize2, Wifi, BarChart3, Trash2,
  LayoutDashboard, Box, Container as ContainerIcon, StickyNote,
} from "lucide-react";
import { HUB_URL, getAuthHeaders } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";

type DetailTab = "overview" | "services" | "containers" | "metrics" | "notes" | "terminal";
const DETAIL_TABS: readonly DetailTab[] = ["overview", "services", "containers", "metrics", "notes", "terminal"] as const;
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

interface MachineData {
  machine: {
    id: string;
    hostname: string;
    ip: string | null;
    os: string | null;
    status: string;
    last_seen: string | null;
    notes?: string;
  };
  metrics: {
    cpu_percent: number;
    cpu_temp_c?: number;
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

  // Liveness derived from last_seen age (120s threshold, matches
  // MachineCard.tsx::classifyMachine). Computed from the actual
  // timestamp, NOT from "SSE map has data for this id" — the snapshot
  // delivers every machine in the fleet, including offline ones, so the
  // prior check force-stamped offline boxes as 'online' the moment you
  // opened their detail page.
  const isLive = useMemo(() => {
    const lastSeenMs = sseData?.last_seen ?? 0;
    if (lastSeenMs <= 0) return false;
    return Date.now() - lastSeenMs < 120_000;
  }, [sseData, tick]);

  const data = useMemo<MachineData | null>(() => {
    if (!baseData) return null;
    if (!sseData) return baseData;

    return {
      ...baseData,
      metrics: {
        cpu_percent: sseData.cpu_percent ?? baseData.metrics.cpu_percent,
        cpu_temp_c: sseData.cpu_temp_c ?? baseData.metrics.cpu_temp_c,
        ram_used_bytes: sseData.ram_used_bytes ?? baseData.metrics.ram_used_bytes,
        ram_total_bytes: sseData.ram_total_bytes ?? baseData.metrics.ram_total_bytes,
        disk_used_bytes: sseData.disk_used_bytes ?? baseData.metrics.disk_used_bytes,
        disk_total_bytes: sseData.disk_total_bytes ?? baseData.metrics.disk_total_bytes,
        gpu_temp: sseData.gpu_temp ?? baseData.metrics.gpu_temp,
        gpu_util_percent: sseData.gpu_util_percent ?? baseData.metrics.gpu_util_percent,
        gpu_vram_used_bytes: sseData.gpu_vram_used_bytes ?? baseData.metrics.gpu_vram_used_bytes,
        gpu_vram_total_bytes: sseData.gpu_vram_total_bytes ?? baseData.metrics.gpu_vram_total_bytes,
      },
      machine: {
        ...baseData.machine,
        // Promote to online ONLY when last_seen says so. Otherwise trust
        // the API status, which itself reflects agent disconnect events.
        status: isLive ? "online" : baseData.machine.status,
        last_seen: sseData.last_seen
          ? new Date(sseData.last_seen).toISOString()
          : baseData.machine.last_seen,
      },
      latency_ms: sseData.latency_ms ?? baseData.latency_ms,
    };
  }, [baseData, sseData, isLive]);

  const effectiveLastUpdated = useMemo(() => {
    // Real last-metric timestamp, never Date.now(). This field tells the
    // user when the agent last reported, not when the page re-rendered.
    if (data?.machine.last_seen) return data.machine.last_seen;
    return lastUpdated;
  }, [data, lastUpdated]);
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
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: "easeOut" }}
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
      {/* Top sticky bar — minimal, navigational only */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-12 flex items-center justify-between">
          <Link
            href="/"
            className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-xs"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
            <span>Fleet</span>
          </Link>

          <div className="flex items-center gap-1.5">
            {isLive && (
              <span className="hidden sm:flex items-center gap-1 text-[10px] text-emerald-400 mr-1">
                <span className="relative flex h-1.5 w-1.5">
                  <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500" />
                </span>
                live
              </span>
            )}
            {effectiveLastUpdated && (
              <span className="hidden sm:inline text-[10px] text-blox-muted font-mono tabular-nums mr-2">
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

      {/* Hero — hostname + status + key facts */}
      <section className="border-b border-blox-border/50 bg-blox-bg/40">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6">
          <div className="flex items-center gap-3">
            <span
              className={[
                "shrink-0 w-2.5 h-2.5 rounded-full",
                status === "online"
                  ? "bg-emerald-500 animate-status-pulse"
                  : status === "warning"
                  ? "bg-amber-500 animate-status-pulse"
                  : "bg-red-500/60",
              ].join(" ")}
              aria-label={`Status: ${status}`}
            />
            <h1 className="text-xl sm:text-2xl font-semibold text-blox-text tracking-tight">
              {machine.hostname}
            </h1>
            <StatusBadge status={status} />
          </div>

          <dl className="flex flex-wrap items-baseline gap-x-5 gap-y-1.5 mt-3 text-[11px]">
            {machine.ip && (
              <div className="flex items-baseline gap-1.5">
                <dt className="text-blox-muted uppercase tracking-[0.08em] text-[9px] font-medium">
                  IP
                </dt>
                <dd className="text-blox-text font-mono tabular-nums">{machine.ip}</dd>
              </div>
            )}
            {machine.os && (
              <div className="flex items-baseline gap-1.5">
                <dt className="text-blox-muted uppercase tracking-[0.08em] text-[9px] font-medium">
                  OS
                </dt>
                <dd className="text-blox-text">{machine.os}</dd>
              </div>
            )}
            {(data.latency_ms ?? 0) > 0 && (
              <div className="flex items-baseline gap-1.5">
                <dt className="text-blox-muted uppercase tracking-[0.08em] text-[9px] font-medium">
                  Latency
                </dt>
                <dd className="text-blox-text font-mono tabular-nums flex items-center gap-1">
                  <Wifi className="w-2.5 h-2.5 text-blox-muted" />
                  {data.latency_ms}ms
                </dd>
              </div>
            )}
            <div className="flex items-baseline gap-1.5">
              <dt className="text-blox-muted uppercase tracking-[0.08em] text-[9px] font-medium">
                ID
              </dt>
              <dd className="text-blox-muted font-mono text-[10px]">{machine.id}</dd>
            </div>
          </dl>
        </div>
      </section>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">
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
            <TabsTrigger value="notes" className="px-4 py-1.5 gap-1.5 text-sm">
              <StickyNote className="w-4 h-4" />
              Notes
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

            {data.hardware_info && <HardwareCard hw={data.hardware_info} />}
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

          <TabsContent value="notes" className="mt-6">
            <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.05 }}>
              {/* PHASE12-NOTE: keying on `notes ?? ""` remounts the
                  component when the server-fetched notes change (e.g.
                  after a refresh) so MachineNotes can avoid the React 19
                  set-state-in-effect anti-pattern. */}
              <MachineNotes
                key={machine.notes ?? ""}
                machineId={id}
                initialNotes={machine.notes ?? ""}
              />
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
                {/* Terminal header — matches HardwareCard / panel rhythm */}
                <div className="flex items-center justify-between px-5 py-3 border-b border-blox-border/50">
                  <div className="flex items-center gap-2.5">
                    <div className="p-1.5 rounded-lg bg-blox-blue/10">
                      <TerminalIcon className="w-3.5 h-3.5 text-blox-blue" />
                    </div>
                    <h3 className="text-sm font-semibold text-blox-text">Terminal</h3>
                    {termState === "active" && (
                      <span className="flex items-center gap-1 text-[10px] text-emerald-400 font-medium ml-1">
                        <span className="relative flex h-1.5 w-1.5">
                          <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                          <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-emerald-500" />
                        </span>
                        connected
                      </span>
                    )}
                    {termState === "connecting" && (
                      <span className="text-[10px] text-blox-blue font-medium ml-1">connecting…</span>
                    )}
                    {termState === "disconnected" && (
                      <span className="text-[10px] text-red-400 font-medium ml-1">disconnected</span>
                    )}
                  </div>
                  <div className="flex items-center gap-1">
                    {termState === "active" && (
                      <>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          onClick={() => setTermExpanded(!termExpanded)}
                          className="text-blox-muted hover:text-blox-text"
                          title={termExpanded ? "Collapse terminal" : "Expand terminal"}
                          aria-label={termExpanded ? "Collapse terminal" : "Expand terminal"}
                        >
                          {termExpanded ? (
                            <Minimize2 className="w-3.5 h-3.5" />
                          ) : (
                            <Maximize2 className="w-3.5 h-3.5" />
                          )}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          onClick={handleTerminalClose}
                          className="text-blox-muted hover:text-red-400"
                          title="Close terminal"
                          aria-label="Close terminal"
                        >
                          <X className="w-3.5 h-3.5" />
                        </Button>
                      </>
                    )}
                    {termState === "disconnected" && (
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => setTermState("pin_entry")}
                        className="text-xs text-blox-blue"
                      >
                        Reconnect
                      </Button>
                    )}
                  </div>
                </div>

                {/* Stable-height body container — prevents pane height jumping between states */}
                <div
                  className="bg-blox-bg/40 transition-all duration-[var(--motion-base)]"
                  style={{
                    minHeight: termState === "active" && termExpanded ? 600 : 360,
                    height: termState === "active" ? (termExpanded ? 600 : 360) : "auto",
                  }}
                >
                  {termState === "locked" && (
                    <div className="flex flex-col items-center justify-center h-[360px] gap-3">
                      <Lock className="w-10 h-10 text-blox-muted/30" />
                      <div className="text-center">
                        <p className="text-sm text-blox-text">Remote Terminal</p>
                        <p className="text-[11px] text-blox-muted mt-1">
                          Enter PIN to unlock terminal access
                        </p>
                      </div>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => isOnline && setTermState("pin_entry")}
                        disabled={!isOnline}
                        className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10 gap-2 mt-2"
                      >
                        <Unlock className="w-3.5 h-3.5" />
                        Unlock Terminal
                      </Button>
                    </div>
                  )}

                  {termState === "pin_entry" && (
                    <div className="flex flex-col items-center justify-center h-[360px] gap-3">
                      <Lock className="w-8 h-8 text-blox-blue/50" />
                      <p className="text-sm text-blox-text">Enter PIN to open terminal</p>
                      <form
                        onSubmit={(e) => {
                          e.preventDefault();
                          handlePinSubmit();
                        }}
                        className="flex items-center gap-2 mt-1"
                      >
                        <Input
                          ref={pinInputRef}
                          type="password"
                          maxLength={8}
                          value={pinInput}
                          onChange={(e) => {
                            setPinInput(e.target.value);
                            setPinError(false);
                          }}
                          placeholder="PIN"
                          className={`w-32 text-center font-mono bg-blox-bg border-blox-border text-blox-text h-9 text-sm ${
                            pinError ? "border-red-500" : ""
                          }`}
                          autoComplete="off"
                        />
                        <Button
                          type="submit"
                          variant="outline"
                          size="sm"
                          className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10"
                        >
                          Open
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setTermState("locked");
                            setPinInput("");
                            setPinError(false);
                          }}
                          className="text-xs text-blox-muted"
                        >
                          Cancel
                        </Button>
                      </form>
                      {pinError && <p className="text-xs text-red-400 mt-1">Invalid PIN</p>}
                    </div>
                  )}

                  {termState === "connecting" && (
                    <div className="flex flex-col items-center justify-center h-[360px] gap-3">
                      <div className="w-6 h-6 border-2 border-blox-blue border-t-transparent rounded-full animate-spin" />
                      <p className="text-sm text-blox-muted">Starting terminal session…</p>
                    </div>
                  )}

                  {termState === "active" && termSessionId && (
                    <div
                      className="h-full"
                      style={{
                        height: termExpanded ? 600 : 360,
                        // Match the xterm background exactly so there's no
                        // seam between xterm's canvas and the wrapper. Reads
                        // the same CSS var that resolvedTheme maps to.
                        background: "var(--surface-base)",
                      }}
                    >
                      <TerminalComponent
                        sessionId={termSessionId}
                        browserToken={termBrowserToken ?? ""}
                        onDisconnect={handleTerminalDisconnect}
                      />
                    </div>
                  )}

                  {termState === "disconnected" && (
                    <div className="flex flex-col items-center justify-center h-[360px] gap-3">
                      <TerminalIcon className="w-8 h-8 text-red-400/40" />
                      <p className="text-sm text-blox-muted">Terminal disconnected</p>
                      <div className="flex items-center gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => setTermState("pin_entry")}
                          className="text-xs text-blox-blue border-blox-blue/20 hover:bg-blox-blue/10"
                        >
                          Reconnect
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setTermState("locked");
                            setTermSessionId(null);
                          }}
                          className="text-xs text-blox-muted"
                        >
                          Close
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              </motion.div>
            </TabsContent>
          )}
        </Tabs>
      </main>
    </motion.div>
  );
}

