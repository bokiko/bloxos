"use client";

import { useState, useMemo, useCallback } from "react";
import { useSSE } from "@/contexts/SSEContext";
import { useAuth } from "@/contexts/AuthContext";
import { demoMachines, MachineMetrics, AlertData } from "@/lib/demo-data";
import { DEMO_MODE, HUB_URL } from "@/lib/session";
import { MachineCard } from "@/components/MachineCard";
import { MachineStatus } from "@/components/StatusBadge";
import { AlertPanel } from "@/components/AlertPanel";
import { AddMachineModal } from "@/components/AddMachineModal";
import { AddAPIMachineModal } from "@/components/AddAPIMachineModal";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table, TableHeader, TableBody, TableRow,
  TableHead, TableCell,
} from "@/components/ui/table";
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator, DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { motion, AnimatePresence } from "framer-motion";
import {
  Bell, Plus, Wifi, WifiOff, Search, LayoutGrid, List,
  ChevronDown, LogOut, Square, CheckSquare, RotateCcw, Trash2,
  ArrowUpDown, Filter, Monitor, Server,
} from "lucide-react";
import Link from "next/link";

type SortOption = "name" | "status" | "cpu" | "gpu_temp";
type StatusFilter = "all" | "online" | "warning" | "offline";
type ViewMode = "grid" | "list";

function getStatus(m: MachineMetrics): MachineStatus {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return "offline";
  if (m.gpu_temp && m.gpu_temp > 80) return "warning";
  const diskPct = (m.disk_total_bytes ?? 0) > 0 ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return "warning";
  const ramPct = (m.ram_total_bytes ?? 0) > 0 ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100 : 0;
  if (ramPct > 95) return "warning";
  return "online";
}

const statusOrder: Record<MachineStatus, number> = {
  offline: 0,
  warning: 1,
  online: 2,
};

function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes === 0) return "0";
  const gb = bytes / (1024 ** 3);
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / (1024 ** 2);
  return `${mb.toFixed(0)} MB`;
}

function timeSince(ms: number): string {
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

const sortLabels: Record<SortOption, string> = {
  name: "Name (A-Z)",
  status: "Status",
  cpu: "CPU %",
  gpu_temp: "GPU Temp",
};

export default function Home() {
  const { machines: liveMachines, connected, hasReceivedData, alertCount, alerts, setAlerts, setAlertCount } = useSSE();
  const { logout, authFetch } = useAuth();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sortBy, setSortBy] = useState<SortOption>("name");
  const [viewMode, setViewMode] = useState<ViewMode>("grid");
  const [tagFilter, setTagFilter] = useState<string | null>(null);
  const [alertPanelOpen, setAlertPanelOpen] = useState(false);
  const [addMachineOpen, setAddMachineOpen] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkLoading, setBulkLoading] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<{id: string; hostname: string} | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [addAPIMachineOpen, setAddAPIMachineOpen] = useState(false);

  const isDemo = DEMO_MODE && !hasReceivedData && liveMachines.length === 0;
  const machines = isDemo ? demoMachines : liveMachines;

  const allTags = useMemo(() => {
    const tagSet = new Set<string>();
    for (const m of machines) {
      if (m.tags) {
        for (const t of m.tags.split(",")) {
          const trimmed = t.trim();
          if (trimmed) tagSet.add(trimmed);
        }
      }
    }
    return Array.from(tagSet).sort();
  }, [machines]);

  const filteredMachines = useMemo(() => {
    let result = machines;

    if (search) {
      const q = search.toLowerCase();
      result = result.filter((m) =>
        m.hostname.toLowerCase().includes(q) ||
        (m.ip && m.ip.toLowerCase().includes(q))
      );
    }

    if (statusFilter !== "all") {
      result = result.filter((m) => getStatus(m) === statusFilter);
    }

    if (tagFilter) {
      result = result.filter((m) =>
        m.tags && m.tags.split(",").map((t) => t.trim()).includes(tagFilter)
      );
    }

    result = [...result].sort((a, b) => {
      switch (sortBy) {
        case "name":
          return a.hostname.localeCompare(b.hostname);
        case "status":
          return statusOrder[getStatus(a)] - statusOrder[getStatus(b)];
        case "cpu":
          return (b.cpu_percent ?? 0) - (a.cpu_percent ?? 0);
        case "gpu_temp":
          return (b.gpu_temp || 0) - (a.gpu_temp || 0);
        default:
          return 0;
      }
    });

    return result;
  }, [machines, search, statusFilter, sortBy, tagFilter]);

  const summary = useMemo(() => {
    let online = 0, warning = 0, offline = 0;
    for (const m of machines) {
      const s = getStatus(m);
      if (s === "online") online++;
      else if (s === "warning") warning++;
      else offline++;
    }
    return { online, warning, offline, total: machines.length };
  }, [machines]);

  const handleAcknowledge = useCallback(async (id: string) => {
    try {
      const res = await authFetch(`${HUB_URL}/api/alerts/${id}/acknowledge`, { method: "POST" });
      if (!res.ok) return;
      setAlerts((prev: AlertData[]) => prev.filter((a: AlertData) => a.id !== id));
      setAlertCount((prev: number) => Math.max(0, prev - 1));
    } catch { /* ignore */ }
  }, [authFetch, setAlerts, setAlertCount]);

  const handleAcknowledgeAll = useCallback(async () => {
    for (const a of alerts) {
      try {
        const res = await authFetch(`${HUB_URL}/api/alerts/${a.id}/acknowledge`, { method: "POST" });
        if (!res.ok) return;
      } catch { /* ignore */ }
    }
    setAlerts([]);
    setAlertCount(0);
  }, [alerts, authFetch, setAlerts, setAlertCount]);

  const toggleSelect = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    if (selected.size === filteredMachines.length) {
      setSelected(new Set());
    } else {
      setSelected(new Set(filteredMachines.map((m) => m.machine_id)));
    }
  }, [selected.size, filteredMachines]);

  const handleBulkReboot = useCallback(async () => {
    if (!confirm(`Reboot ${selected.size} machine(s)? This cannot be undone.`)) return;
    setBulkLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: Array.from(selected),
          type: "reboot",
          target: "",
        }),
      });
      if (!res.ok) return;
    } catch { /* ignore */ }
    setBulkLoading(false);
    setSelected(new Set());
  }, [selected, authFetch]);

  const handleBulkRestart = useCallback(async (service: string) => {
    setBulkLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: Array.from(selected),
          type: "restart_service",
          target: service,
        }),
      });
      if (!res.ok) return;
    } catch { /* ignore */ }
    setBulkLoading(false);
    setSelected(new Set());
  }, [selected, authFetch]);

  const handleDeleteFromGrid = useCallback(async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/machines/${deleteTarget.id}`, { method: "DELETE" });
      if (!res.ok) return;
    } catch { /* ignore */ }
    setDeleteLoading(false);
    setDeleteTarget(null);
  }, [deleteTarget, authFetch]);

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.3 }}
      className="min-h-screen bg-blox-bg"
    >
      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-bold tracking-tight">
              <span className="text-blox-blue">Blox</span>
              <span className="text-blox-text">OS</span>
            </h1>
            {isDemo && (
              <Badge variant="outline" className="text-[10px] border-amber-500/30 bg-amber-500/10 text-amber-400 h-auto py-0 px-2">
                Demo
              </Badge>
            )}
          </div>

          <div className="hidden sm:flex items-center gap-4 text-xs">
            <span className="flex items-center gap-1.5">
              <span className="relative flex h-2 w-2">
                <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-500" />
              </span>
              <span className="text-blox-muted font-mono tabular-nums">{summary.online} online</span>
            </span>
            {summary.warning > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-amber-500" />
                <span className="text-blox-muted font-mono tabular-nums">{summary.warning} warning</span>
              </span>
            )}
            {summary.offline > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-red-500/60" />
                <span className="text-blox-muted font-mono tabular-nums">{summary.offline} offline</span>
              </span>
            )}
          </div>

          <div className="flex items-center gap-1.5">
            <div className="flex items-center gap-1 mr-1">
              {connected ? (
                <Wifi className="w-3.5 h-3.5 text-emerald-400" />
              ) : (
                <WifiOff className="w-3.5 h-3.5 text-red-400" />
              )}
            </div>

            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => setAlertPanelOpen(true)}
              className="relative text-blox-muted hover:text-blox-text"
            >
              <Bell className="w-4 h-4" />
              {alertCount > 0 && (
                <span className="absolute -top-0.5 -right-0.5 w-4 h-4 rounded-full bg-red-500 text-[9px] flex items-center justify-center text-white font-bold">
                  {alertCount > 9 ? "9+" : alertCount}
                </span>
              )}
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={() => setAddMachineOpen(true)}
              className="text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 text-xs gap-1.5"
            >
              <Plus className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">Add Machine</span>
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={() => setAddAPIMachineOpen(true)}
              className="text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 text-xs gap-1.5"
            >
              <Server className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">Add API</span>
            </Button>

            <Button
              variant="ghost"
              size="icon-sm"
              onClick={logout}
              className="text-blox-muted hover:text-blox-text"
              title="Logout"
            >
              <LogOut className="w-4 h-4" />
            </Button>
          </div>
        </div>
      </header>

      {/* Bulk action bar */}
      <AnimatePresence>
        {selected.size > 0 && viewMode === "list" && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            className="bg-blox-blue/5 border-b border-blox-blue/20 px-4 sm:px-6 py-2 overflow-hidden"
          >
            <div className="max-w-[1600px] mx-auto flex items-center gap-3">
              <span className="text-xs text-blox-blue font-medium font-mono tabular-nums">{selected.size} selected</span>
              <Button
                variant="outline"
                size="xs"
                onClick={() => handleBulkRestart("ollama")}
                disabled={bulkLoading}
                className="text-xs border-blox-border text-blox-text"
              >
                Restart Ollama
              </Button>
              <Button
                variant="outline"
                size="xs"
                onClick={() => handleBulkRestart("docker")}
                disabled={bulkLoading}
                className="text-xs border-blox-border text-blox-text"
              >
                Restart Docker
              </Button>
              <Button
                variant="destructive"
                size="xs"
                onClick={handleBulkReboot}
                disabled={bulkLoading}
                className="text-xs gap-1"
              >
                <RotateCcw className="w-3 h-3" />
                Reboot All
              </Button>
              <button
                onClick={() => setSelected(new Set())}
                className="text-xs text-blox-muted hover:text-blox-text ml-auto transition-colors"
              >
                Clear
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Search + Filter Bar */}
      <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[200px] max-w-sm">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-blox-muted" />
            <Input
              type="text"
              placeholder="Search machines..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-9 h-8 text-xs bg-blox-card border-blox-border text-blox-text placeholder:text-blox-muted/50"
            />
          </div>

          {/* Status filter dropdown */}
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant="outline" size="sm" className="text-xs border-blox-border text-blox-text gap-1.5">
                  <Filter className="w-3 h-3" />
                  {statusFilter === "all" ? "All Status" : statusFilter.charAt(0).toUpperCase() + statusFilter.slice(1)}
                  <ChevronDown className="w-3 h-3 text-blox-muted" />
                </Button>
              }
            />
            <DropdownMenuContent align="start" className="bg-blox-card border-blox-border">
              <DropdownMenuItem onClick={() => setStatusFilter("all")} className="text-xs text-blox-text">All Status</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("online")} className="text-xs text-emerald-400">Online</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("warning")} className="text-xs text-amber-400">Warning</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("offline")} className="text-xs text-red-400">Offline</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          {/* Sort dropdown */}
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button variant="outline" size="sm" className="text-xs border-blox-border text-blox-text gap-1.5">
                  <ArrowUpDown className="w-3 h-3" />
                  {sortLabels[sortBy]}
                  <ChevronDown className="w-3 h-3 text-blox-muted" />
                </Button>
              }
            />
            <DropdownMenuContent align="start" className="bg-blox-card border-blox-border">
              <DropdownMenuLabel className="text-blox-muted">Sort by</DropdownMenuLabel>
              <DropdownMenuSeparator className="bg-blox-border" />
              {(Object.keys(sortLabels) as SortOption[]).map((key) => (
                <DropdownMenuItem key={key} onClick={() => setSortBy(key)} className="text-xs text-blox-text">
                  {sortLabels[key]}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          {allTags.length > 0 && (
            <div className="flex items-center gap-1.5 flex-wrap">
              {tagFilter && (
                <button
                  onClick={() => setTagFilter(null)}
                  className="text-[10px] px-2 py-1 rounded-full bg-blox-blue/10 text-blox-blue border border-blox-blue/20 hover:bg-blox-blue/20 transition-colors font-medium"
                >
                  Clear tag
                </button>
              )}
              {allTags.map((tag) => (
                <button
                  key={tag}
                  onClick={() => setTagFilter(tagFilter === tag ? null : tag)}
                  className={`text-[10px] px-2 py-1 rounded-full border transition-colors font-medium ${
                    tagFilter === tag
                      ? "bg-blox-blue/20 text-blox-blue border-blox-blue/30"
                      : "bg-blox-card text-blox-muted border-blox-border hover:border-blox-muted/30"
                  }`}
                >
                  {tag}
                </button>
              ))}
            </div>
          )}

          <div className="flex-1" />

          <div className="flex items-center rounded-lg border border-blox-border overflow-hidden">
            <button
              onClick={() => setViewMode("grid")}
              className={`p-2 transition-colors ${
                viewMode === "grid"
                  ? "bg-blox-blue/10 text-blox-blue"
                  : "text-blox-muted hover:text-blox-text"
              }`}
              title="Grid view"
            >
              <LayoutGrid className="w-3.5 h-3.5" />
            </button>
            <button
              onClick={() => setViewMode("list")}
              className={`p-2 transition-colors ${
                viewMode === "list"
                  ? "bg-blox-blue/10 text-blox-blue"
                  : "text-blox-muted hover:text-blox-text"
              }`}
              title="List view"
            >
              <List className="w-3.5 h-3.5" />
            </button>
          </div>

          <span className="text-[10px] text-blox-muted font-mono tabular-nums">
            {filteredMachines.length} machine{filteredMachines.length !== 1 ? "s" : ""}
          </span>
        </div>
      </div>

      {/* Content */}
      <main className="max-w-[1600px] mx-auto px-4 sm:px-6 pb-6">
        {viewMode === "grid" ? (
          <motion.div
            className="grid gap-4"
            style={{ gridTemplateColumns: "repeat(auto-fill, minmax(300px, 1fr))" }}
          >
            <AnimatePresence mode="popLayout">
              {filteredMachines.map((m, i) => (
                <motion.div
                  key={m.machine_id}
                  layout
                  initial={{ opacity: 0, y: 20 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, scale: 0.95 }}
                  transition={{ duration: 0.3, delay: i * 0.03 }}
                >
                  <MachineCard machine={m} onClick={() => {}} onDelete={(id, hostname) => setDeleteTarget({id, hostname})} />
                </motion.div>
              ))}
            </AnimatePresence>
          </motion.div>
        ) : (
          <div className="bg-blox-card border border-blox-border rounded-xl overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow className="border-b-blox-border hover:bg-transparent">
                  <TableHead className="w-8 text-blox-muted">
                    <button onClick={toggleSelectAll} className="text-blox-muted hover:text-blox-text">
                      {selected.size === filteredMachines.length && filteredMachines.length > 0 ? (
                        <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                      ) : (
                        <Square className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </TableHead>
                  <TableHead className="text-blox-muted text-xs">Status</TableHead>
                  <TableHead className="text-blox-muted text-xs">Hostname</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden md:table-cell">IP</TableHead>
                  <TableHead className="text-blox-muted text-xs">CPU</TableHead>
                  <TableHead className="text-blox-muted text-xs">RAM</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden lg:table-cell">GPU</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden lg:table-cell">VRAM</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Latency</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Last Seen</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Tags</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredMachines.map((m, i) => {
                  const status = getStatus(m);
                  const ramPct = (m.ram_total_bytes ?? 0) > 0 ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100 : 0;
                  const tags = m.tags ? m.tags.split(",").filter((t) => t.trim()) : [];
                  const dotColor = status === "online" ? "bg-emerald-500" : status === "warning" ? "bg-amber-500" : "bg-red-500/50";
                  const isSelected = selected.has(m.machine_id);

                  return (
                    <TableRow
                      key={m.machine_id}
                      className={`border-b-blox-border/30 hover:bg-blox-border/10 transition-colors ${isSelected ? "bg-blox-blue/5" : i % 2 === 1 ? "bg-blox-bg/30" : ""}`}
                    >
                      <TableCell className="text-xs">
                        <button onClick={(e) => { e.stopPropagation(); toggleSelect(m.machine_id); }} className="text-blox-muted hover:text-blox-text">
                          {isSelected ? (
                            <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                          ) : (
                            <Square className="w-3.5 h-3.5" />
                          )}
                        </button>
                      </TableCell>
                      <TableCell className="text-xs">
                        <Link href={`/machine/${m.machine_id}`}>
                          <span className={`inline-block w-2 h-2 rounded-full ${dotColor} ${status === "online" ? "shadow-sm shadow-emerald-500/50" : ""}`} />
                        </Link>
                      </TableCell>
                      <TableCell className="text-xs font-medium text-blox-text">
                        <Link href={`/machine/${m.machine_id}`} className="hover:text-blox-blue transition-colors">{m.hostname}</Link>
                      </TableCell>
                      <TableCell className="text-xs text-blox-muted font-mono tabular-nums hidden md:table-cell">{m.ip || "-"}</TableCell>
                      <TableCell className="text-xs">
                        <span className={`tabular-nums font-mono ${
                          (m.cpu_percent ?? 0) > 85 ? "text-red-400" : (m.cpu_percent ?? 0) > 60 ? "text-amber-400" : "text-blox-text"
                        }`}>{(m.cpu_percent ?? 0).toFixed(0)}%</span>
                      </TableCell>
                      <TableCell className="text-xs">
                        <span className="tabular-nums font-mono text-blox-text">{ramPct.toFixed(0)}%</span>
                        <span className="text-blox-muted ml-1 font-mono">{formatBytes(m.ram_used_bytes)}</span>
                      </TableCell>
                      <TableCell className="text-xs hidden lg:table-cell">
                        {m.gpu_temp ? (
                          <span className={`tabular-nums font-mono ${
                            m.gpu_temp > 80 ? "text-red-400" : m.gpu_temp > 70 ? "text-amber-400" : "text-blox-text"
                          }`}>{m.gpu_temp}&deg;C</span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs hidden lg:table-cell">
                        {m.gpu_vram_total_bytes && m.gpu_vram_total_bytes > 0 ? (
                          <span className="tabular-nums font-mono text-blox-text">
                            {formatBytes(m.gpu_vram_used_bytes || 0)}/{formatBytes(m.gpu_vram_total_bytes)}
                          </span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs hidden xl:table-cell">
                        {m.latency_ms && m.latency_ms > 0 ? (
                          <span className="tabular-nums font-mono text-blox-muted">{m.latency_ms}ms</span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs text-blox-muted hidden xl:table-cell font-mono tabular-nums">
                        {m.last_seen ? timeSince(m.last_seen) : "never"}
                      </TableCell>
                      <TableCell className="text-xs hidden xl:table-cell">
                        <div className="flex gap-1 flex-wrap">
                          {tags.map((tag) => (
                            <Badge key={tag} variant="outline" className="text-[9px] px-1.5 h-auto py-0 border-blox-border text-blox-muted">
                              {tag}
                            </Badge>
                          ))}
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
            {filteredMachines.length === 0 && (
              <div className="py-12 text-center text-blox-muted text-sm">No machines match filters</div>
            )}
          </div>
        )}

        {viewMode === "grid" && filteredMachines.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-blox-muted">
            <Monitor className="w-12 h-12 mb-4 opacity-20" />
            <p className="text-sm">No machines match filters</p>
          </div>
        )}
      </main>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null); }}>
        <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-md" showCloseButton={false}>
          <DialogHeader>
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-red-500/10">
                <Trash2 className="w-5 h-5 text-red-400" />
              </div>
              <DialogTitle className="text-blox-text">Delete Machine</DialogTitle>
            </div>
            <DialogDescription className="text-blox-muted text-xs mt-2">
              Are you sure you want to remove <span className="text-blox-text font-medium">{deleteTarget?.hostname}</span> from BloxOS? This will delete all historical data for this machine.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="bg-transparent border-t-blox-border">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteTarget(null)}
              disabled={deleteLoading}
              className="text-xs text-blox-muted border-blox-border"
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={handleDeleteFromGrid}
              disabled={deleteLoading}
              className="text-xs"
            >
              {deleteLoading ? "Deleting..." : "Delete Machine"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertPanel
        open={alertPanelOpen}
        onClose={() => setAlertPanelOpen(false)}
        alerts={alerts}
        onAcknowledge={handleAcknowledge}
        onAcknowledgeAll={handleAcknowledgeAll}
      />

      <AddMachineModal
        open={addMachineOpen}
        onClose={() => setAddMachineOpen(false)}
      />

      <AddAPIMachineModal
        open={addAPIMachineOpen}
        onClose={() => setAddAPIMachineOpen(false)}
      />
    </motion.div>
  );
}
