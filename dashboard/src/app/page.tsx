"use client";

import { useState, useMemo, useCallback, useEffect } from "react";
import { useSSE } from "@/contexts/SSEContext";
import { useAuth } from "@/contexts/AuthContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import { SaveFilterButton } from "@/components/SaveFilterButton";
import { SavedFiltersDropdown } from "@/components/SavedFiltersDropdown";
import { demoMachines, MachineMetrics, AlertData } from "@/lib/demo-data";
import { DEMO_MODE, HUB_URL } from "@/lib/session";
import { MachineCard, MachineCardSkeleton } from "@/components/MachineCard";
import { MachineStatus, classifyMachine, STATUS_ORDER } from "@/components/StatusBadge";
import { Sparkline } from "@/components/Sparkline";
import { AlertPanel } from "@/components/AlertPanel";
import { AddMachineModal } from "@/components/AddMachineModal";
import { AddAPIMachineModal, type EditableAPIMachine } from "@/components/AddAPIMachineModal";
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
  Plus, WifiOff, Search, LayoutGrid, List,
  ChevronDown, Square, CheckSquare, RotateCcw, Trash2, Pencil,
  ArrowUpDown, Filter, Monitor, Server, RefreshCw, Boxes,
  GitCompareArrows, Bot,
} from "lucide-react";
import Link from "next/link";
import { ThemeToggle } from "@/components/ThemeToggle";
import { CommandPalette, useCommandPaletteHotkey } from "@/components/CommandPalette";
import { FleetPulse } from "@/components/FleetPulse";
import { NeedsAttention } from "@/components/NeedsAttention";
import { UserMenu } from "@/components/UserMenu";
import { BrandedHeader } from "@/components/BrandedHeader";
import { FleetOverview } from "@/components/FleetOverview";

type SortOption = "name" | "status" | "cpu" | "gpu_temp";
type StatusFilter = "all" | "live" | "warning" | "critical" | "offline" | "stale";
type ViewMode = "grid" | "list";

function getStatus(m: MachineMetrics): MachineStatus {
  return classifyMachine(m).status;
}

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
  const { machines: liveMachines, connected, hasReceivedData, alerts, setAlerts, setAlertCount, refreshMachine, refreshFleet } = useSSE();
  const { authFetch, hasScope } = useAuth();
  const canCreateInstallTokens = hasScope("install_tokens.admin");
  const canManageAPIMachines = hasScope("api_machines.admin");
  const canControlFleet = hasScope("fleet.control");
  const canDeleteMachines = hasScope("fleet.admin");
  // Phase 11 — hydrate viewMode/sortBy from per-user preferences. The
  // PreferencesContext lazy-init reads from localStorage so the defaults
  // are correct on first paint after a reload (no flash).
  const { preferences, updateScalar } = usePreferences();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sortBy, setSortBy] = useState<SortOption>(() => preferences.default_sort);
  const [viewMode, setViewMode] = useState<ViewMode>(() => preferences.default_view);
  const [tagFilter, setTagFilter] = useState<string | null>(null);

  // Keep local state in sync if preferences change post-mount (e.g. user
  // changes default in another tab/device, then we refresh from the
  // server). This is a legitimate external→React sync — the same pattern
  // used by BrandingContext / PreferencesContext, suppressed for the same
  // reason.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setViewMode(preferences.default_view);
  }, [preferences.default_view]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setSortBy(preferences.default_sort);
  }, [preferences.default_sort]);

  const changeView = useCallback(
    (next: ViewMode) => {
      setViewMode(next);
      void updateScalar({ default_view: next });
    },
    [updateScalar],
  );

  const changeSort = useCallback(
    (next: SortOption) => {
      setSortBy(next);
      void updateScalar({ default_sort: next });
    },
    [updateScalar],
  );

  const applySavedFilter = useCallback(
    (f: { search?: unknown; statusFilter?: unknown; tagFilter?: unknown; sortBy?: unknown }) => {
      if (typeof f.search === "string") setSearch(f.search);
      if (
        f.statusFilter === "all" ||
        f.statusFilter === "live" ||
        f.statusFilter === "warning" ||
        f.statusFilter === "critical" ||
        f.statusFilter === "offline" ||
        f.statusFilter === "stale" ||
        f.statusFilter === "online"
      ) {
        setStatusFilter(f.statusFilter === "online" ? "live" : f.statusFilter);
      }
      setTagFilter(typeof f.tagFilter === "string" ? f.tagFilter : null);
      if (f.sortBy === "name" || f.sortBy === "status" || f.sortBy === "cpu" || f.sortBy === "gpu_temp") {
        changeSort(f.sortBy);
      }
    },
    [changeSort],
  );
  const [alertPanelOpen, setAlertPanelOpen] = useState(false);
  const [addMachineOpen, setAddMachineOpen] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkLoading, setBulkLoading] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<{id: string; hostname: string} | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [addAPIMachineOpen, setAddAPIMachineOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  useCommandPaletteHotkey(setCommandOpen);
  const [editAPIMachine, setEditAPIMachine] = useState<EditableAPIMachine | null>(null);
  const [apiMachines, setApiMachines] = useState<EditableAPIMachine[]>([]);

  const isDemo = DEMO_MODE && !hasReceivedData && liveMachines.length === 0;
  const machines = isDemo ? demoMachines : liveMachines;

  const loadAPIMachines = useCallback(async () => {
    try {
      const res = await authFetch(`${HUB_URL}/api/api-machines`);
      if (!res.ok) return;
      const data = await res.json();
      setApiMachines(Array.isArray(data) ? data : []);
    } catch {
      // ignore
    }
  }, [authFetch]);

  useEffect(() => {
    let active = true;
    const run = async () => {
      try {
        const res = await authFetch(`${HUB_URL}/api/api-machines`);
        if (!res.ok || !active) return;
        const data = await res.json();
        if (active) {
          setApiMachines(Array.isArray(data) ? data : []);
        }
      } catch {
        // ignore
      }
    };
    void run();
    return () => {
      active = false;
    };
  }, [authFetch]);

  const apiMachineByMachineID = useMemo(
    () => new Map(apiMachines.map((machine) => [`api-${machine.id}`, machine])),
    [apiMachines]
  );

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
    // Drop records that don't have enough shape to render — avoids the whole
    // list crashing on a single malformed SSE update.
    let result = machines.filter((m) => m && typeof m.machine_id === "string");

    if (search) {
      const q = search.toLowerCase();
      result = result.filter((m) =>
        (m.hostname ?? "").toLowerCase().includes(q) ||
        (m.ip ?? "").toLowerCase().includes(q)
      );
    }

    if (statusFilter !== "all") {
      result = result.filter((m) => getStatus(m) === statusFilter);
    }

    if (tagFilter) {
      result = result.filter((m) =>
        m.tags ? m.tags.split(",").map((t) => t.trim()).includes(tagFilter) : false
      );
    }

    const primaryCompare = (a: MachineMetrics, b: MachineMetrics) => {
      switch (sortBy) {
        case "name":
          return (a.hostname ?? "").localeCompare(b.hostname ?? "");
        case "status":
          return STATUS_ORDER[getStatus(a)] - STATUS_ORDER[getStatus(b)];
        case "cpu":
          return (b.cpu_percent ?? 0) - (a.cpu_percent ?? 0);
        case "gpu_temp":
          return (b.gpu_temp || 0) - (a.gpu_temp || 0);
        default:
          return 0;
      }
    };

    // Composite ordering — applied as a single comparator so each priority
    // is strictly higher than the next:
    //   1. status severity (critical → warning → offline → stale → live)
    //   2. pinned before unpinned within the same status bucket
    //   3. user's chosen sort key within the same status+pin bucket
    //
    // Done as one sort to avoid the bug from the prior two-phase impl,
    // where a healthy-pinned machine could outrank a critical-unpinned
    // one — directly contradicting the PR's "problem-first" promise.
    const pinnedSet = new Set(preferences.pinned_machines);
    result = [...result].sort((a, b) => {
      const statusDiff = STATUS_ORDER[getStatus(a)] - STATUS_ORDER[getStatus(b)];
      if (statusDiff !== 0) return statusDiff;

      const pinDiff = Number(pinnedSet.has(b.machine_id)) - Number(pinnedSet.has(a.machine_id));
      if (pinDiff !== 0) return pinDiff;

      return primaryCompare(a, b);
    });

    return result;
  }, [machines, search, statusFilter, sortBy, tagFilter, preferences.pinned_machines]);


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
      if (deleteTarget.id.startsWith("api-")) {
        const apiID = deleteTarget.id.replace(/^api-/, "");
        setApiMachines((prev) => prev.filter((machine) => machine.id !== apiID));
      }
    } catch { /* ignore */ }
    setDeleteLoading(false);
    setDeleteTarget(null);
  }, [deleteTarget, authFetch]);

  const handleAPIMachineSaved = useCallback(() => {
    void loadAPIMachines();
  }, [loadAPIMachines]);

  const openEditAPIMachine = useCallback((machineID: string) => {
    const machine = apiMachineByMachineID.get(machineID);
    if (machine) {
      setEditAPIMachine(machine);
    }
  }, [apiMachineByMachineID]);

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.25 }}
      className="min-h-screen bg-blox-bg"
    >
      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between gap-4">
          {/* Brand */}
          <div className="flex items-center gap-3 shrink-0">
            <BrandedHeader size="compact" />
            {isDemo && (
              <Badge variant="outline" className="text-[10px] border-amber-500/30 bg-amber-500/10 text-amber-400 h-auto py-0 px-2">
                Demo
              </Badge>
            )}
          </div>

          {/* Spacer — claims the middle so left and right groups stay anchored */}
          <div className="flex-1" />

          {/* Action group */}
          <div className="flex items-center gap-1">
            {/* Phase 7: global refresh — sends refresh_metrics to every connected agent */}
            <GlobalRefreshButton onRefresh={refreshFleet} disabled={!canControlFleet} />

            {/* Theme toggle (Phase 1) */}
            <ThemeToggle />

            {/* Inventory link (Phase 6 Unit B) */}
            <Link
              href="/inventory"
              className="inline-flex items-center justify-center rounded-md w-8 h-8 text-blox-muted hover:text-blox-text hover:bg-blox-border/50 transition-colors"
              title="Hardware inventory"
              aria-label="Hardware inventory"
            >
              <Boxes className="w-4 h-4" />
            </Link>

            <Link
              href="/versions"
              className="inline-flex items-center justify-center rounded-md w-8 h-8 text-blox-muted hover:text-blox-text hover:bg-blox-border/50 transition-colors"
              title="Agent versions"
              aria-label="Agent versions"
            >
              <GitCompareArrows className="w-4 h-4" />
            </Link>

            <Link
              href="/sessions"
              className="inline-flex items-center justify-center rounded-md w-8 h-8 text-blox-muted hover:text-blox-text hover:bg-blox-border/50 transition-colors"
              title="AI Sessions"
              aria-label="AI Sessions"
            >
              <Bot className="w-4 h-4" />
            </Link>

            {/* Vertical divider — separates utility icons from create actions */}
            <div className="w-px h-5 bg-blox-border/50 mx-1.5" aria-hidden />

            {/* Add API — secondary, ghost variant, only for admins */}
            {canManageAPIMachines && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setAddAPIMachineOpen(true)}
                className="text-blox-muted hover:text-blox-text gap-1.5 text-xs hidden sm:inline-flex"
                title="Add API-polled machine (Proxmox / Synology)"
              >
                <Server className="w-3.5 h-3.5" />
                API
              </Button>
            )}

            {/* Add Machine — PRIMARY action, filled accent button */}
            {canCreateInstallTokens && (
              <Button
                size="sm"
                onClick={() => setAddMachineOpen(true)}
                className="bg-blox-blue text-white hover:bg-blox-blue/90 gap-1.5 text-xs"
                title="Add a machine via install token"
              >
                <Plus className="w-3.5 h-3.5" />
                <span className="hidden sm:inline">Add Machine</span>
                <span className="sm:hidden">Add</span>
              </Button>
            )}

            {/* Vertical divider — separates create actions from account */}
            <div className="w-px h-5 bg-blox-border/50 mx-1.5" aria-hidden />

            {/* User menu (replaces Users link + Logout) */}
            <UserMenu />
          </div>
        </div>
      </header>

      {/* Fleet Pulse Strip — sits directly below the header */}
      <FleetPulse onAlertsClick={() => setAlertPanelOpen(true)} />

      {/* Needs-Attention stripe — only renders when problems exist */}
      <NeedsAttention machines={machines} />

      {/* Fleet Overview — gauges and resource attribution */}
      {machines.length > 0 && hasReceivedData && (
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 pt-6">
          <FleetOverview machines={machines} />
        </div>
      )}

      {/* Degraded connection banner — surfaces SSE disconnect inline so it isn't
          easy to miss behind the small wifi icon in the header. */}
      <AnimatePresence>
        {!connected && !isDemo && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            className="bg-amber-500/5 border-b border-amber-500/20 px-4 sm:px-6 py-2 overflow-hidden"
          >
            <div className="max-w-[1600px] mx-auto flex items-center gap-2 text-xs text-amber-400">
              <WifiOff className="w-3.5 h-3.5" />
              <span>Live updates paused — reconnecting to hub…</span>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Bulk action bar */}
      <AnimatePresence>
        {selected.size > 0 && viewMode === "list" && canControlFleet && (
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
              className="pl-9 pr-14 h-8 text-xs bg-blox-card border-blox-border text-blox-text placeholder:text-blox-muted/50"
            />
            <button
              type="button"
              onClick={() => setCommandOpen(true)}
              className="absolute right-2 top-1/2 -translate-y-1/2 hidden sm:flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-mono text-blox-muted bg-blox-bg border border-blox-border rounded hover:text-blox-text transition-colors"
              aria-label="Open command palette"
              title="Open command palette (⌘K)"
            >
              <span>⌘</span>
              <span>K</span>
            </button>
          </div>

          {/* Phase 11 — saved filter quick-apply (only when at least one exists). */}
          {preferences.saved_filters.length > 0 && (
            <SavedFiltersDropdown onApply={applySavedFilter} />
          )}

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
              <DropdownMenuItem onClick={() => setStatusFilter("live")} className="text-xs text-status-ok">Live</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("warning")} className="text-xs text-status-warning">Warning</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("critical")} className="text-xs text-status-critical">Critical</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("offline")} className="text-xs text-status-offline">Offline</DropdownMenuItem>
              <DropdownMenuItem onClick={() => setStatusFilter("stale")} className="text-xs text-status-stale">Stale</DropdownMenuItem>
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
                <DropdownMenuItem key={key} onClick={() => changeSort(key)} className="text-xs text-blox-text">
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

          {/* Phase 11 — save current filter as a named saved filter. Only
              shown when filters are actually active so the bar stays clean. */}
          {(search || statusFilter !== "all" || tagFilter) && (
            <SaveFilterButton
              currentFilter={{ search, statusFilter, tagFilter, sortBy }}
              disabled={!canControlFleet && preferences.saved_filters.length >= 20}
            />
          )}

          <div className="flex items-center rounded-lg border border-blox-border overflow-hidden">
            <button
              onClick={() => changeView("grid")}
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
              onClick={() => changeView("list")}
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
            className="grid auto-rows-fr justify-start"
            style={{
              gap: "var(--grid-gap)",
              gridTemplateColumns: "repeat(auto-fill, minmax(var(--grid-min-col), 360px))",
            }}
          >
            {!hasReceivedData && machines.length === 0 && !isDemo && (
              <>
                {Array.from({ length: 4 }).map((_, i) => (
                  <MachineCardSkeleton key={`skeleton-${i}`} />
                ))}
              </>
            )}

            <AnimatePresence mode="popLayout">
              {filteredMachines.map((m, i) => (
                <motion.div
                  key={m.machine_id}
                  layout
                  initial={{ opacity: 0, y: 20 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, scale: 0.95 }}
                  transition={{ duration: 0.25, delay: i * 0.03 }}
                  className="h-full"
                >
                  <MachineCard
                    machine={m}
                    onDelete={canDeleteMachines ? (id, hostname) => setDeleteTarget({ id, hostname }) : undefined}
                    onEdit={canManageAPIMachines ? openEditAPIMachine : undefined}
                    onRefresh={canControlFleet ? refreshMachine : undefined}
                  />
                </motion.div>
              ))}
            </AnimatePresence>
          </motion.div>
        ) : (
          <div className="bg-blox-card border border-blox-border rounded-xl overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow className="border-b-blox-border hover:bg-transparent">
                  {canControlFleet && (
                    <TableHead className="w-8 text-blox-muted">
                      <button onClick={toggleSelectAll} className="text-blox-muted hover:text-blox-text">
                        {selected.size === filteredMachines.length && filteredMachines.length > 0 ? (
                          <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                        ) : (
                          <Square className="w-3.5 h-3.5" />
                        )}
                      </button>
                    </TableHead>
                  )}
                  <TableHead className="text-blox-muted text-xs">Status</TableHead>
                  <TableHead className="text-blox-muted text-xs">Hostname</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden md:table-cell">IP</TableHead>
                  <TableHead className="text-blox-muted text-xs">CPU</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden lg:table-cell">30m</TableHead>
                  <TableHead className="text-blox-muted text-xs">RAM</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden md:table-cell">Disk</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden lg:table-cell">GPU</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden lg:table-cell">VRAM</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Latency</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Last Seen</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">OS</TableHead>
                  <TableHead className="text-blox-muted text-xs hidden xl:table-cell">Tags</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredMachines.map((m, i) => {
                  const status = getStatus(m);
                  const ramPct = (m.ram_total_bytes ?? 0) > 0 ? ((m.ram_used_bytes ?? 0) / m.ram_total_bytes) * 100 : 0;
                  const diskPct = (m.disk_total_bytes ?? 0) > 0 ? ((m.disk_used_bytes ?? 0) / m.disk_total_bytes) * 100 : 0;
                  const tags = m.tags ? m.tags.split(",").filter((t) => t.trim()) : [];
                  const dotColor =
                    status === "live" ? "bg-status-ok" :
                    status === "stale" ? "bg-status-stale" :
                    status === "warning" ? "bg-status-warning" :
                    status === "critical" ? "bg-status-critical" :
                    "bg-status-offline";
                  const isSelected = selected.has(m.machine_id);
                  const adapterTag = tags.find((tag) => ["synology", "proxmox"].includes(tag.trim().toLowerCase()));
                  const isAPIMachine = !!adapterTag;

                  return (
                    <TableRow
                      key={m.machine_id}
                      className={`border-b-blox-border/30 hover:bg-blox-border/10 transition-colors ${isSelected ? "bg-blox-blue/5" : i % 2 === 1 ? "bg-blox-bg/30" : ""}`}
                    >
                      {canControlFleet && (
                        <TableCell className="text-xs">
                          <button onClick={(e) => { e.stopPropagation(); toggleSelect(m.machine_id); }} className="text-blox-muted hover:text-blox-text">
                            {isSelected ? (
                              <CheckSquare className="w-3.5 h-3.5 text-blox-blue" />
                            ) : (
                              <Square className="w-3.5 h-3.5" />
                            )}
                          </button>
                        </TableCell>
                      )}
                      <TableCell className="text-xs">
                        <Link href={`/machine/${m.machine_id}`}>
                          <span className={`inline-block w-2 h-2 rounded-full ${dotColor} ${status === "live" ? "shadow-sm shadow-status-ok/50" : ""}`} />
                        </Link>
                      </TableCell>
                      <TableCell className="text-xs font-medium text-blox-text">
                        <div className="flex items-center gap-2">
                          <Link href={`/machine/${m.machine_id}`} className="hover:text-blox-blue transition-colors">{m.hostname}</Link>
                          {isAPIMachine && canManageAPIMachines && (
                            <button
                              onClick={(e) => {
                                e.preventDefault();
                                e.stopPropagation();
                                openEditAPIMachine(m.machine_id);
                              }}
                              className="text-blox-muted hover:text-blox-blue transition-colors"
                              title="Edit API machine"
                            >
                              <Pencil className="w-3.5 h-3.5" />
                            </button>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-xs text-blox-muted font-mono tabular-nums hidden md:table-cell">{m.ip || "-"}</TableCell>
                      <TableCell className="text-xs">
                        <span className={`tabular-nums font-mono ${
                          (m.cpu_percent ?? 0) > 85 ? "text-red-400" : (m.cpu_percent ?? 0) > 60 ? "text-amber-400" : "text-blox-text"
                        }`}>{(m.cpu_percent ?? 0).toFixed(0)}%</span>
                      </TableCell>
                      <TableCell className="hidden lg:table-cell">
                        {status !== "offline" && (
                          <Sparkline machineId={m.machine_id} width={90} height={20} />
                        )}
                      </TableCell>
                      <TableCell className="text-xs">
                        <span className="tabular-nums font-mono text-blox-text">{ramPct.toFixed(0)}%</span>
                        <span className="text-blox-muted ml-1 font-mono tabular-nums">{formatBytes(m.ram_used_bytes)}</span>
                      </TableCell>
                      <TableCell className="text-xs hidden md:table-cell">
                        {(m.disk_total_bytes ?? 0) > 0 && (
                          <>
                            <span className={`tabular-nums font-mono ${
                              diskPct > 90 ? "text-red-400" : diskPct > 75 ? "text-amber-400" : "text-blox-text"
                            }`}>{diskPct.toFixed(0)}%</span>
                            <span className="text-blox-muted ml-1 font-mono tabular-nums">{formatBytes(m.disk_used_bytes)}</span>
                          </>
                        )}
                      </TableCell>
                      <TableCell className="text-xs hidden lg:table-cell">
                        {m.gpu_temp ? (
                          <span className={`tabular-nums font-mono ${
                            m.gpu_temp > 80 ? "text-red-400" : m.gpu_temp > 70 ? "text-amber-400" : "text-blox-text"
                          }`}>{m.gpu_temp}&deg;C</span>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs hidden lg:table-cell">
                        {m.gpu_vram_total_bytes && m.gpu_vram_total_bytes > 0 ? (
                          <span className="tabular-nums font-mono text-blox-text">
                            {formatBytes(m.gpu_vram_used_bytes || 0)}/{formatBytes(m.gpu_vram_total_bytes)}
                          </span>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs hidden xl:table-cell">
                        {m.latency_ms && m.latency_ms > 0 ? (
                          <span className="tabular-nums font-mono text-blox-muted">{m.latency_ms}ms</span>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-xs text-blox-muted hidden xl:table-cell font-mono tabular-nums">
                        {m.last_seen ? timeSince(m.last_seen) : "never"}
                      </TableCell>
                      <TableCell className="text-xs hidden xl:table-cell">
                        {m.os && (
                          <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-blox-border/50 text-blox-muted font-mono">
                            {m.os}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="text-xs hidden xl:table-cell">
                        <div className="flex gap-1 flex-wrap">
                          {tags.map((tag) => (
                            <Badge key={tag} variant="outline" className="text-[10px] px-1.5 h-auto py-0 border-blox-border text-blox-muted">
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

        {viewMode === "grid" && filteredMachines.length === 0 && hasReceivedData && (
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

      {addAPIMachineOpen && (
        <AddAPIMachineModal
          open={addAPIMachineOpen}
          onClose={() => setAddAPIMachineOpen(false)}
          onSaved={handleAPIMachineSaved}
        />
      )}

      {editAPIMachine && (
        <AddAPIMachineModal
          open={!!editAPIMachine}
          machine={editAPIMachine}
          onClose={() => setEditAPIMachine(null)}
          onSaved={handleAPIMachineSaved}
        />
      )}

      <CommandPalette
        open={commandOpen}
        onOpenChange={setCommandOpen}
        onAddMachine={canCreateInstallTokens ? () => setAddMachineOpen(true) : undefined}
        onAddAPIMachine={canManageAPIMachines ? () => setAddAPIMachineOpen(true) : undefined}
        onOpenAlerts={() => setAlertPanelOpen(true)}
      />
    </motion.div>
  );
}

/* ============================================================================
 * GlobalRefreshButton — Phase 7
 *
 * Header-mounted refresh affordance that broadcasts a refresh_metrics
 * command to every connected agent. Disabled for viewer-role users (the
 * backend gates POST /api/refresh on fleet.control); the visual state
 * mirrors that.
 * ============================================================================ */

function GlobalRefreshButton({
  onRefresh,
  disabled,
}: {
  onRefresh: () => Promise<void>;
  disabled: boolean;
}) {
  const [refreshing, setRefreshing] = useState(false);

  const handleClick = async () => {
    if (refreshing || disabled) return;
    setRefreshing(true);
    await onRefresh();
    // 2s is enough for fleets up to ~30 machines to receive their refreshed
    // metrics via SSE. Longer than the per-card timeout because we're
    // waiting on every connected agent, not just one.
    setTimeout(() => setRefreshing(false), 2000);
  };

  return (
    <Button
      variant="ghost"
      size="icon-sm"
      onClick={handleClick}
      disabled={disabled || refreshing}
      className="text-blox-muted hover:text-blox-text"
      title={disabled ? "Refresh requires operator role" : "Refresh fleet metrics"}
      aria-label="Refresh fleet metrics"
    >
      <RefreshCw className={`w-4 h-4 ${refreshing ? "animate-spin" : ""}`} />
    </Button>
  );
}
