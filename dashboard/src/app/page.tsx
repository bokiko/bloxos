"use client";

import { useState, useMemo, useCallback } from "react";
import { useFleetSSE } from "@/hooks/useFleetSSE";
import { useAuth } from "@/contexts/AuthContext";
import { demoMachines, MachineMetrics, AlertData } from "@/lib/demo-data";
import { MachineCard } from "@/components/MachineCard";
import { MachineStatus } from "@/components/StatusBadge";
import { AlertPanel } from "@/components/AlertPanel";
import { AddMachineModal } from "@/components/AddMachineModal";
import {
  Bell, Plus, Wifi, WifiOff, Search, LayoutGrid, List,
  ChevronDown, LogOut, Square, CheckSquare, RotateCcw,
} from "lucide-react";
import Link from "next/link";

type SortOption = "name" | "status" | "cpu" | "gpu_temp";
type StatusFilter = "all" | "online" | "warning" | "offline";
type ViewMode = "grid" | "list";

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

function getStatus(m: MachineMetrics): MachineStatus {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return "offline";
  if (m.gpu_temp && m.gpu_temp > 80) return "warning";
  const diskPct = m.disk_total_bytes > 0 ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return "warning";
  const ramPct = m.ram_total_bytes > 0 ? (m.ram_used_bytes / m.ram_total_bytes) * 100 : 0;
  if (ramPct > 95) return "warning";
  return "online";
}

const statusOrder: Record<MachineStatus, number> = {
  offline: 0,
  warning: 1,
  online: 2,
};

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0";
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

export default function Home() {
  const { machines: liveMachines, connected, hasReceivedData, alertCount, alerts, setAlerts, setAlertCount } = useFleetSSE();
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

  const machines = hasReceivedData && liveMachines.length > 0
    ? liveMachines
    : demoMachines;

  const isDemo = !hasReceivedData || liveMachines.length === 0;

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
          return b.cpu_percent - a.cpu_percent;
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
    return { online, warning, offline };
  }, [machines]);

  const handleAcknowledge = useCallback(async (id: string) => {
    try {
      await authFetch(`${HUB_URL}/api/alerts/${id}/acknowledge`, { method: "POST" });
      setAlerts((prev: AlertData[]) => prev.filter((a: AlertData) => a.id !== id));
      setAlertCount((prev: number) => Math.max(0, prev - 1));
    } catch { /* ignore */ }
  }, [authFetch, setAlerts, setAlertCount]);

  const handleAcknowledgeAll = useCallback(async () => {
    for (const a of alerts) {
      try {
        await authFetch(`${HUB_URL}/api/alerts/${a.id}/acknowledge`, { method: "POST" });
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
      await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: Array.from(selected),
          type: "reboot",
          target: "",
        }),
      });
    } catch { /* ignore */ }
    setBulkLoading(false);
    setSelected(new Set());
  }, [selected, authFetch]);

  const handleBulkRestart = useCallback(async (service: string) => {
    setBulkLoading(true);
    try {
      await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: Array.from(selected),
          type: "restart_service",
          target: service,
        }),
      });
    } catch { /* ignore */ }
    setBulkLoading(false);
    setSelected(new Set());
  }, [selected, authFetch]);

  return (
    <div className="min-h-screen bg-blox-bg">
      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-md border-b border-blox-border">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-bold tracking-tight">
              <span className="text-blox-blue">Blox</span>
              <span className="text-blox-text">OS</span>
            </h1>
            {isDemo && (
              <span className="text-[10px] px-2 py-0.5 rounded-full bg-blox-amber/10 text-blox-amber border border-blox-amber/20">
                Demo Mode
              </span>
            )}
          </div>

          <div className="hidden sm:flex items-center gap-4 text-xs">
            <span className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-blox-green" />
              <span className="text-blox-muted">{summary.online} online</span>
            </span>
            {summary.warning > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-blox-amber" />
                <span className="text-blox-muted">{summary.warning} warning</span>
              </span>
            )}
            {summary.offline > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-blox-red" />
                <span className="text-blox-muted">{summary.offline} offline</span>
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            <div className="flex items-center gap-1 mr-2">
              {connected ? (
                <Wifi className="w-3.5 h-3.5 text-blox-green" />
              ) : (
                <WifiOff className="w-3.5 h-3.5 text-blox-red" />
              )}
            </div>

            <button
              onClick={() => setAlertPanelOpen(true)}
              className="relative p-2 rounded-md hover:bg-blox-border/50 transition-colors"
            >
              <Bell className="w-4 h-4 text-blox-muted" />
              {alertCount > 0 && (
                <span className="absolute -top-0.5 -right-0.5 w-4 h-4 rounded-full bg-blox-red text-[9px] flex items-center justify-center text-white font-bold">
                  {alertCount > 9 ? "9+" : alertCount}
                </span>
              )}
            </button>

            <button
              onClick={() => setAddMachineOpen(true)}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-blox-blue/10 text-blox-blue text-xs hover:bg-blox-blue/20 transition-colors border border-blox-blue/20"
            >
              <Plus className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">Add Machine</span>
            </button>

            <button
              onClick={logout}
              className="p-2 rounded-md hover:bg-blox-border/50 transition-colors"
              title="Logout"
            >
              <LogOut className="w-4 h-4 text-blox-muted" />
            </button>
          </div>
        </div>
      </header>

      {/* Bulk action bar */}
      {selected.size > 0 && viewMode === "list" && (
        <div className="bg-blox-blue/5 border-b border-blox-blue/20 px-4 sm:px-6 py-2">
          <div className="max-w-[1600px] mx-auto flex items-center gap-3">
            <span className="text-xs text-blox-blue font-medium">{selected.size} selected</span>
            <button
              onClick={() => handleBulkRestart("ollama")}
              disabled={bulkLoading}
              className="text-xs px-3 py-1.5 rounded-md bg-blox-card border border-blox-border text-blox-text hover:border-blox-blue/40 transition-colors disabled:opacity-50"
            >
              Restart Ollama
            </button>
            <button
              onClick={() => handleBulkRestart("docker")}
              disabled={bulkLoading}
              className="text-xs px-3 py-1.5 rounded-md bg-blox-card border border-blox-border text-blox-text hover:border-blox-blue/40 transition-colors disabled:opacity-50"
            >
              Restart Docker
            </button>
            <button
              onClick={handleBulkReboot}
              disabled={bulkLoading}
              className="text-xs px-3 py-1.5 rounded-md bg-blox-red/10 border border-blox-red/20 text-blox-red hover:bg-blox-red/20 transition-colors disabled:opacity-50"
            >
              <RotateCcw className="w-3 h-3 inline mr-1" />
              Reboot All
            </button>
            <button
              onClick={() => setSelected(new Set())}
              className="text-xs text-blox-muted hover:text-blox-text ml-auto transition-colors"
            >
              Clear
            </button>
          </div>
        </div>
      )}

      {/* Search + Filter Bar */}
      <div className="max-w-[1600px] mx-auto px-4 sm:px-6 py-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[200px] max-w-sm">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-blox-muted" />
            <input
              type="text"
              placeholder="Search machines..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-3 py-2 text-xs rounded-md bg-blox-card border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
            />
          </div>

          <div className="relative">
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
              className="appearance-none pl-3 pr-8 py-2 text-xs rounded-md bg-blox-card border border-blox-border text-blox-text focus:outline-none focus:border-blox-blue/50 cursor-pointer"
            >
              <option value="all">All Status</option>
              <option value="online">Online</option>
              <option value="warning">Warning</option>
              <option value="offline">Offline</option>
            </select>
            <ChevronDown className="absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-blox-muted pointer-events-none" />
          </div>

          <div className="relative">
            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value as SortOption)}
              className="appearance-none pl-3 pr-8 py-2 text-xs rounded-md bg-blox-card border border-blox-border text-blox-text focus:outline-none focus:border-blox-blue/50 cursor-pointer"
            >
              <option value="name">Sort: Name (A-Z)</option>
              <option value="status">Sort: Status</option>
              <option value="cpu">Sort: CPU %</option>
              <option value="gpu_temp">Sort: GPU Temp</option>
            </select>
            <ChevronDown className="absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-blox-muted pointer-events-none" />
          </div>

          {allTags.length > 0 && (
            <div className="flex items-center gap-1.5 flex-wrap">
              {tagFilter && (
                <button
                  onClick={() => setTagFilter(null)}
                  className="text-[10px] px-2 py-1 rounded-full bg-blox-blue/10 text-blox-blue border border-blox-blue/20 hover:bg-blox-blue/20 transition-colors"
                >
                  Clear tag
                </button>
              )}
              {allTags.map((tag) => (
                <button
                  key={tag}
                  onClick={() => setTagFilter(tagFilter === tag ? null : tag)}
                  className={`text-[10px] px-2 py-1 rounded-full border transition-colors ${
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

          <div className="flex items-center rounded-md border border-blox-border overflow-hidden">
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

          <span className="text-[10px] text-blox-muted">
            {filteredMachines.length} machine{filteredMachines.length !== 1 ? "s" : ""}
          </span>
        </div>
      </div>

      {/* Content */}
      <main className="max-w-[1600px] mx-auto px-4 sm:px-6 pb-6">
        {viewMode === "grid" ? (
          <div className="grid gap-4" style={{
            gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))"
          }}>
            {filteredMachines.map((m) => (
              <MachineCard key={m.machine_id} machine={m} onClick={() => {}} />
            ))}
          </div>
        ) : (
          <div className="bg-blox-card border border-blox-border rounded-lg overflow-hidden">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-blox-border text-left">
                  <th className="px-4 py-3 w-8">
                    <button onClick={toggleSelectAll} className="text-blox-muted hover:text-blox-text">
                      {selected.size === filteredMachines.length && filteredMachines.length > 0 ? (
                        <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                      ) : (
                        <Square className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </th>
                  <th className="px-4 py-3 text-blox-muted font-medium">Status</th>
                  <th className="px-4 py-3 text-blox-muted font-medium">Hostname</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden md:table-cell">IP</th>
                  <th className="px-4 py-3 text-blox-muted font-medium">CPU</th>
                  <th className="px-4 py-3 text-blox-muted font-medium">RAM</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden lg:table-cell">GPU</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden lg:table-cell">VRAM</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden xl:table-cell">Latency</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden xl:table-cell">Last Seen</th>
                  <th className="px-4 py-3 text-blox-muted font-medium hidden xl:table-cell">Tags</th>
                </tr>
              </thead>
              <tbody>
                {filteredMachines.map((m) => {
                  const status = getStatus(m);
                  const ramPct = m.ram_total_bytes > 0 ? (m.ram_used_bytes / m.ram_total_bytes) * 100 : 0;
                  const tags = m.tags ? m.tags.split(",").filter((t) => t.trim()) : [];
                  const dotColor = status === "online" ? "bg-blox-green" : status === "warning" ? "bg-blox-amber" : "bg-blox-red";
                  const isSelected = selected.has(m.machine_id);

                  return (
                    <tr key={m.machine_id} className={`border-b border-blox-border/50 hover:bg-blox-border/20 transition-colors ${isSelected ? "bg-blox-blue/5" : ""}`}>
                      <td className="px-4 py-3">
                        <button onClick={(e) => { e.stopPropagation(); toggleSelect(m.machine_id); }} className="text-blox-muted hover:text-blox-text">
                          {isSelected ? (
                            <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                          ) : (
                            <Square className="w-3.5 h-3.5" />
                          )}
                        </button>
                      </td>
                      <td className="px-4 py-3">
                        <Link href={`/machine/${m.machine_id}`}>
                          <span className={`inline-block w-2 h-2 rounded-full ${dotColor}`} />
                        </Link>
                      </td>
                      <td className="px-4 py-3 font-medium text-blox-text">
                        <Link href={`/machine/${m.machine_id}`}>{m.hostname}</Link>
                      </td>
                      <td className="px-4 py-3 text-blox-muted font-mono hidden md:table-cell">{m.ip || "-"}</td>
                      <td className="px-4 py-3">
                        <span className={`tabular-nums ${
                          m.cpu_percent > 85 ? "text-blox-red" : m.cpu_percent > 60 ? "text-blox-amber" : "text-blox-text"
                        }`}>{m.cpu_percent.toFixed(0)}%</span>
                      </td>
                      <td className="px-4 py-3">
                        <span className="tabular-nums text-blox-text">{ramPct.toFixed(0)}%</span>
                        <span className="text-blox-muted ml-1">{formatBytes(m.ram_used_bytes)}</span>
                      </td>
                      <td className="px-4 py-3 hidden lg:table-cell">
                        {m.gpu_temp ? (
                          <span className={`tabular-nums ${
                            m.gpu_temp > 80 ? "text-blox-red" : m.gpu_temp > 70 ? "text-blox-amber" : "text-blox-text"
                          }`}>{m.gpu_temp}&deg;C</span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </td>
                      <td className="px-4 py-3 hidden lg:table-cell">
                        {m.gpu_vram_total_bytes && m.gpu_vram_total_bytes > 0 ? (
                          <span className="tabular-nums text-blox-text">
                            {formatBytes(m.gpu_vram_used_bytes || 0)}/{formatBytes(m.gpu_vram_total_bytes)}
                          </span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </td>
                      <td className="px-4 py-3 hidden xl:table-cell">
                        {m.latency_ms && m.latency_ms > 0 ? (
                          <span className="tabular-nums text-blox-muted">{m.latency_ms}ms</span>
                        ) : (
                          <span className="text-blox-muted">-</span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-blox-muted hidden xl:table-cell">
                        {m.last_seen ? timeSince(m.last_seen) : "never"}
                      </td>
                      <td className="px-4 py-3 hidden xl:table-cell">
                        <div className="flex gap-1 flex-wrap">
                          {tags.map((tag) => (
                            <span key={tag} className="text-[9px] px-1.5 py-0.5 rounded bg-blox-border text-blox-muted">
                              {tag}
                            </span>
                          ))}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {filteredMachines.length === 0 && (
              <div className="py-12 text-center text-blox-muted text-sm">No machines match filters</div>
            )}
          </div>
        )}

        {viewMode === "grid" && filteredMachines.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-blox-muted">
            <MonitorIcon className="w-12 h-12 mb-4 opacity-30" />
            <p className="text-sm">No machines match filters</p>
          </div>
        )}
      </main>

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
    </div>
  );
}

function MonitorIcon(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" {...props}>
      <rect width="20" height="14" x="2" y="3" rx="2"/>
      <line x1="8" x2="16" y1="21" y2="21"/>
      <line x1="12" x2="12" y1="17" y2="21"/>
    </svg>
  );
}
