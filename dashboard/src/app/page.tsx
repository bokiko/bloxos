"use client";

/* ============================================================================
 * Overview — the fleet's home page.
 *
 * One page, one shape. There is no layout switch any more: AppShell owns the
 * rail, the page title, refresh, ⌘K, alerts, Add API, Add Machine, the theme
 * toggle and the user menu, so nothing in here draws chrome.
 *
 * The page reads top to bottom as one argument:
 *   1. the date,
 *   2. fleet availability beside fleet power,
 *   3. the single Machine fleet work surface and all of its tooling.
 *
 * There is no explanatory prose anywhere on it. Every sentence the page used
 * to open with — the posture claim, the availability summary, the paragraph
 * under the power chart — restated a number that was already on screen and
 * cost a line of vertical space on every visit to do it. Figures and short
 * labels only; the long-form caveats live in `title` tooltips and in
 * docs/power-history.md.
 *
 * It used to be four panes over two rows — availability beside a "needs
 * attention" list, and power beside a highest-load ranking. Four dense panels
 * above a dense table is more than the page can carry: it read as a wall, and
 * the two rankings restated, in a second and third shape, what the table
 * underneath them already says per machine. Two panes, one row. The attention
 * list's job is not dropped — availability's "Needs review" and "Offline"
 * rows are now buttons into the alerts sheet, which is also on the shell's
 * top bar on every route.
 *
 * This controller holds the state and the handlers; the sections above are
 * presentational components under components/overview/.
 * ========================================================================== */

import { useState, useMemo, useCallback, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { WifiOff, RotateCcw, Trash2, Monitor } from "lucide-react";

import { useSSE } from "@/contexts/SSEContext";
import { useAuth } from "@/contexts/AuthContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import { demoMachines, type MachineMetrics, type AlertData } from "@/lib/demo-data";
import { DEMO_MODE, HUB_URL } from "@/lib/session";
import { orderMachines } from "@/lib/machine-order.mjs";
import { bulkCommandFeedback, selectionAfterBulkAttempt } from "@/lib/command-feedback.mjs";

import { AppShell } from "@/components/shell/AppShell";
import { OPEN_ALERTS, OPEN_COMMAND, API_MACHINES_CHANGED } from "@/components/shell/ShellActions";
import { classifyMachine, STATUS_ORDER, type MachineStatus } from "@/components/StatusBadge";
import type { LoadBaselines } from "@/components/fleet/fleetModel";
import { MachineCard, MachineCardSkeleton } from "@/components/MachineCard";
import { AlertPanel } from "@/components/AlertPanel";
import { AddAPIMachineModal, type EditableAPIMachine } from "@/components/AddAPIMachineModal";
import { ArrangeMachinesDialog } from "@/components/ArrangeMachinesDialog";
import { useToast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";

import { OverviewIntro } from "@/components/overview/OverviewIntro";
import {
  FleetAvailabilityPanel,
  type AvailabilityCounts,
} from "@/components/overview/FleetAvailabilityPanel";
import { FleetPowerPane } from "@/components/overview/FleetPowerPane";
import {
  MachineFleetToolbar,
  type SortOption,
  type StatusFilter,
  type ViewMode,
} from "@/components/overview/MachineFleetToolbar";
import { MachineFleetTable } from "@/components/overview/MachineFleetTable";
import { MachineNoteDialog } from "@/components/overview/MachineNoteDialog";
import { Disclosure } from "@/components/overview/Disclosure";
import { useWorkspacePrefs } from "@/components/overview/useWorkspacePrefs";
import { useMachineNotes } from "@/components/overview/useMachineNotes";

/* POSTURE — the one paired-panel row.
   `.mf-pane-grid` in monoform.css: two equal `minmax(0,1fr)` columns, an
   EXPANDED panel given a common minimum height, stacked below 900px. Equal
   halves, so neither of the two reads as the subordinate one.

   The 288px floor is kept as-is. With four panes it was what stopped a short
   pane looking like a stub beside a tall one; with these two it is inert in
   the normal case — the power pane's chart, readouts and cost row run well
   past it and availability's own content lands around the same height. Where
   it still earns its keep is the degenerate case: an empty fleet, or a hub
   with no power counters at all, where FleetPowerPane draws an empty state
   that is deliberately sized to its content and CENTRED in the slack this
   floor creates. Lower the floor and that empty state hugs the header and
   reads as a chart that failed to load; raise it and an empty fleet gets two
   panels of mostly nothing. Availability keeps its content top-aligned and
   lets the difference fall as whitespace at the bottom, exactly as it did
   next to the taller attention panel before.

   WHEN ONE OF A PAIR IS FOLDED, THE FOLDED ONE KEEPS ITS COLUMN. It shrinks to
   a single line at the top of its cell (`align-self: start`) and its open
   partner keeps its own half and sets the row height. The alternative — the
   open pane widening to span both columns — was rejected for three reasons:
   the pane nobody touched would re-lay out its chart as a side effect of a
   click elsewhere; folding and unfolding would shuttle the page between a one-
   and a two-column layout; and the point of the equal columns is that the
   panes read as one symmetric block, which a reflow to full width breaks the
   moment anybody folds anything. What you folded stays where you left it,
   ready to unfold in place. */

// Severity for one machine under the reader's baseline policy. classifyMachine
// (lib/fleet-metrics.mjs) is the single place status is decided; `baselines`
// only ever suppresses the CPU-saturation rule, so an expected-high-load
// machine still counts as offline, stale, or critical on RAM/disk/GPU here.
function getStatus(m: MachineMetrics, baselines: LoadBaselines): MachineStatus {
  return classifyMachine(m, { expectedHighCpu: baselines.has(m.machine_id) }).status;
}

function OverviewContent() {
  const { addToast } = useToast();
  const {
    machines: liveMachines,
    connected,
    hasReceivedData,
    alerts,
    setAlerts,
    setAlertCount,
    refreshMachine,
  } = useSSE();
  const { authFetch, hasScope, token } = useAuth();

  const canManageAPIMachines = hasScope("api_machines.admin");
  const canControlFleet = hasScope("fleet.control");
  const canDeleteMachines = hasScope("fleet.admin");
  // Same scope the machine detail page's Notes tab gates its editor on.
  const canEditNotes = hasScope("fleet.metadata");

  // Phase 11 — hydrate viewMode/sortBy from per-user preferences. The
  // PreferencesContext lazy-init reads from localStorage so the defaults
  // are correct on first paint after a reload (no flash).
  const { preferences, updateScalar, saveMachineOrder, loading: preferencesLoading } = usePreferences();
  // Collapsed sections, the power window and tariff, and the expected-high-load
  // flags. Browser-local per user — see components/overview/useWorkspacePrefs.
  // The tariff is read here and set in Settings → Preferences, so this page
  // takes `powerRate` without its setter.
  const { isCollapsed, toggleSection, baselines, toggleBaseline, powerPeriod, setPowerPeriod, powerRate } =
    useWorkspacePrefs();
  const [arrangeOpen, setArrangeOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sortBy, setSortBy] = useState<SortOption>(() => preferences.default_sort);
  const [viewMode, setViewMode] = useState<ViewMode>(() => preferences.default_view);
  const [tagFilter, setTagFilter] = useState<string | null>(null);

  // One clock for the whole page. classifyMachine() and statusOf() read
  // Date.now() internally, so machines only age to stale/offline when
  // something re-renders — this tick is that something, and it is threaded
  // into every memo that depends on freshness.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 5_000);
    return () => clearInterval(id);
  }, []);

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
      void updateScalar({ default_sort: next }).catch(() => addToast("error", "Sort changed locally but could not be saved. Please retry."));
    },
    [updateScalar, addToast],
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
      if (f.sortBy === "manual" || f.sortBy === "name" || f.sortBy === "status" || f.sortBy === "cpu" || f.sortBy === "gpu_temp") {
        changeSort(f.sortBy);
      }
    },
    [changeSort],
  );

  // Lazy-init from a cross-route ?panel=alerts request. Safe to read window
  // here: this controller only mounts client-side, so there is no hydration
  // mismatch and no setState-in-effect.
  const [alertPanelOpen, setAlertPanelOpen] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return new URLSearchParams(window.location.search).get("panel") === "alerts";
  });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkLoading, setBulkLoading] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; hostname: string } | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [editAPIMachine, setEditAPIMachine] = useState<EditableAPIMachine | null>(null);
  const [apiMachines, setApiMachines] = useState<EditableAPIMachine[]>([]);

  const isDemo = DEMO_MODE && !hasReceivedData && liveMachines.length === 0;
  const source = isDemo ? demoMachines : liveMachines;

  // Drop records that don't have enough shape to render — avoids the whole
  // page crashing on a single malformed SSE update.
  const machines = useMemo(
    () => source.filter((m) => m && typeof m.machine_id === "string"),
    [source],
  );

  // Machine notes, read per machine from the hub — see useMachineNotes for why
  // that is a fan-out and what it costs. Disabled in demo mode: demo machines
  // do not exist on any hub, so asking about their notes would produce a burst
  // of 404s and a table full of "unavailable".
  const machineIDs = useMemo(() => machines.map((m) => m.machine_id), [machines]);
  const notes = useMachineNotes(machineIDs, !isDemo);
  const [noteTarget, setNoteTarget] = useState<{ id: string; hostname: string } | null>(null);

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

  // Shell-forwarded events: open the alert panel in place (same-route),
  // pick up a cross-route request via /?panel=alerts on mount, and refresh the
  // API-machine config list after any shell-owned Add API save so the list
  // stays consistent wherever the add happened.
  useEffect(() => {
    const openAlerts = () => setAlertPanelOpen(true);
    const reloadAPI = () => void loadAPIMachines();
    window.addEventListener(OPEN_ALERTS, openAlerts);
    window.addEventListener(API_MACHINES_CHANGED, reloadAPI);
    // The panel was already opened by the lazy initial state; just tidy the
    // URL so a refresh doesn't reopen it.
    if (new URLSearchParams(window.location.search).get("panel") === "alerts") {
      window.history.replaceState(null, "", window.location.pathname);
    }
    return () => {
      window.removeEventListener(OPEN_ALERTS, openAlerts);
      window.removeEventListener(API_MACHINES_CHANGED, reloadAPI);
    };
  }, [loadAPIMachines]);

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

  /* -- Fleet posture ------------------------------------------------------ */

  const counts = useMemo<AvailabilityCounts>(() => {
    let live = 0, warning = 0, critical = 0, stale = 0, offline = 0;
    for (const m of machines) {
      switch (getStatus(m, baselines)) {
        case "live": live += 1; break;
        case "warning": warning += 1; break;
        case "critical": critical += 1; break;
        case "stale": stale += 1; break;
        case "offline": offline += 1; break;
      }
    }
    return {
      total: machines.length,
      connected: machines.length - offline,
      live, warning, critical, stale, offline,
      needsReview: warning + critical + stale,
    };
    // classifyMachine reads the wall clock; the tick is the deliberate dep.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, now, baselines]);

  /* -- Machine fleet ------------------------------------------------------ */

  const filteredMachines = useMemo(() => {
    let result = machines;

    if (search) {
      const q = search.toLowerCase();
      result = result.filter((m) =>
        (m.hostname ?? "").toLowerCase().includes(q) ||
        (m.ip ?? "").toLowerCase().includes(q)
      );
    }

    if (statusFilter !== "all") {
      result = result.filter((m) => getStatus(m, baselines) === statusFilter);
    }

    if (tagFilter) {
      result = result.filter((m) =>
        m.tags ? m.tags.split(",").map((t) => t.trim()).includes(tagFilter) : false
      );
    }

    return orderMachines(result, {
      sort: sortBy,
      order: preferences.machine_order,
      pinned: preferences.pinned_machines,
      status: m => STATUS_ORDER[getStatus(m, baselines)],
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machines, now, search, statusFilter, sortBy, tagFilter, baselines, preferences.pinned_machines, preferences.machine_order]);

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
    const attempted = Array.from(selected);
    setBulkLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: attempted,
          type: "reboot",
          target: "",
        }),
      });
      const feedback = bulkCommandFeedback(res.ok, await res.json(), attempted.length, "reboot");
      addToast(feedback.type, feedback.message);
    } catch {
      addToast("error", "Request interrupted; completion is unknown. Check machine status before retrying.");
    } finally {
      // Clear exactly the attempted machines on EVERY finished attempt —
      // success, partial failure, or interrupted — so nothing is silently
      // retried; selections made while in flight are preserved.
      setBulkLoading(false);
      setSelected((prev) => selectionAfterBulkAttempt(prev, attempted));
    }
  }, [selected, authFetch, addToast]);

  const handleBulkRestart = useCallback(async (service: string) => {
    const attempted = Array.from(selected);
    setBulkLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/bulk/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          machine_ids: attempted,
          type: "restart_service",
          target: service,
        }),
      });
      const feedback = bulkCommandFeedback(res.ok, await res.json(), attempted.length, "restart_service");
      addToast(feedback.type, feedback.message);
    } catch {
      addToast("error", "Request interrupted; completion is unknown. Check machine status before retrying.");
    } finally {
      setBulkLoading(false);
      setSelected((prev) => selectionAfterBulkAttempt(prev, attempted));
    }
  }, [selected, authFetch, addToast]);

  const handleDeleteFromGrid = useCallback(async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/machines/${deleteTarget.id}`, { method: "DELETE" });
      if (!res.ok) {
        addToast("error", `Delete failed (HTTP ${res.status}). ${deleteTarget.hostname} was not removed.`);
        return;
      }
      if (deleteTarget.id.startsWith("api-")) {
        const apiID = deleteTarget.id.replace(/^api-/, "");
        setApiMachines((prev) => prev.filter((machine) => machine.id !== apiID));
      }
      // Agent machines disappear via the hub's machine_removed SSE event.
    } catch {
      addToast("error", `Delete failed: request interrupted. ${deleteTarget.hostname} may still exist — refresh to check.`);
    } finally {
      setDeleteLoading(false);
      setDeleteTarget(null);
    }
  }, [deleteTarget, authFetch, addToast]);

  const handleAPIMachineSaved = useCallback(() => {
    void loadAPIMachines();
  }, [loadAPIMachines]);

  const openEditAPIMachine = useCallback((machineID: string) => {
    const machine = apiMachineByMachineID.get(machineID);
    if (machine) {
      setEditAPIMachine(machine);
    }
  }, [apiMachineByMachineID]);

  const requestDelete = useCallback((id: string, hostname: string) => {
    setDeleteTarget({ id, hostname });
  }, []);

  const openNote = useCallback(
    (id: string) => {
      const machine = machines.find((m) => m.machine_id === id);
      setNoteTarget({ id, hostname: machine?.hostname || id });
    },
    [machines],
  );

  const openAlertPanel = useCallback(() => setAlertPanelOpen(true), []);
  const openCommandPalette = useCallback(
    () => window.dispatchEvent(new CustomEvent(OPEN_COMMAND)),
    [],
  );

  const showBulkBar = selected.size > 0 && viewMode === "list" && canControlFleet;

  // A folded fleet still reports its size, and says so honestly when a filter
  // is narrowing it — otherwise collapsing would hide the fact that most of
  // the fleet is filtered out.
  const fleetOpen = !isCollapsed("fleet");
  const fleetSummary =
    filteredMachines.length === machines.length
      ? `${machines.length} ${machines.length === 1 ? "machine" : "machines"}`
      : `${filteredMachines.length} of ${machines.length} machines`;

  return (
    <>
      <OverviewIntro />

      {isDemo && (
        <p className="mb-6 inline-flex items-center gap-2 rounded-[10px] border border-status-warning/30 bg-status-warning-tint px-3 py-1.5 text-[12px] text-status-warning">
          <span className="mf-status-dot mf-status-warning" aria-hidden="true" />
          Demo data — not connected to a hub.
        </p>
      )}

      {/* Degraded connection banner — surfaces SSE disconnect inline so it
          isn't easy to miss behind a small icon in the chrome. */}
      <AnimatePresence>
        {!connected && !isDemo && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            className="mb-6 overflow-hidden rounded-[10px] border border-status-warning/30 bg-status-warning-tint"
          >
            <div className="flex items-center gap-2 px-3 py-2 text-xs text-status-warning">
              <WifiOff className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Live updates paused — reconnecting to hub…</span>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* B — fleet posture: how much of the fleet is up, and what it is
             drawing. The two rows in the availability pane that count
             something wrong open the alerts sheet — the same sheet, with the
             same acknowledge actions, that the shell's bell opens. */}
      <div className="mf-pane-grid">
        <FleetAvailabilityPanel
          counts={counts}
          onOpenAlerts={openAlertPanel}
          open={!isCollapsed("availability")}
          onToggle={() => toggleSection("availability")}
        />
        {/* The section id stays "capacity": it is the persistence key for the
            collapse state, and renaming it would silently unfold this pane for
            every operator who had folded it. */}
        <FleetPowerPane
          open={!isCollapsed("capacity")}
          onToggle={() => toggleSection("capacity")}
          period={powerPeriod}
          onPeriodChange={setPowerPeriod}
          rate={powerRate}
        />
      </div>

      {/* C — the one machine work surface.
          The section had a second, larger line under the kicker ("Every
          machine, and everything you can do to it"). It restated what a table
          of machines with buttons on it evidently is, so the kicker is now the
          whole heading — and, since it is also the disclosure toggle, it is
          the thing that folds the section away.

          It is a `.mf-section` like the four panes above it, so it is a panel
          while it is open and one quiet line on a hairline once it is folded.
          The table inside therefore draws its own inset frame rather than a
          second panel around the first.

          The top margin shrinks with the section. 44px of air above a panel is
          separation between two blocks; 44px above a single folded line is
          just the space the fold was supposed to reclaim. */}
      <section
        className={`mf-section ${fleetOpen ? "mt-11" : "mt-6"}`}
        aria-labelledby="machine-fleet-heading"
        data-open={fleetOpen ? "true" : "false"}
      >
        <Disclosure
          id="fleet"
          label="Machine fleet"
          headingID="machine-fleet-heading"
          open={fleetOpen}
          onToggle={() => toggleSection("fleet")}
          summary={fleetSummary}
        >
          <AnimatePresence>
            {showBulkBar && (
              <motion.div
                initial={{ height: 0, opacity: 0 }}
                animate={{ height: "auto", opacity: 1 }}
                exit={{ height: 0, opacity: 0 }}
                className="overflow-hidden"
              >
                <div className="mt-5 flex flex-wrap items-center gap-3 rounded-[10px] border border-accent/30 bg-accent-subtle px-3 py-2">
                  <span className="mf-metric text-xs font-medium text-accent">
                    {selected.size} selected
                  </span>
                  <Button
                    variant="outline"
                    size="xs"
                    onClick={() => handleBulkRestart("ollama")}
                    disabled={bulkLoading}
                    className="border-border-subtle text-xs text-text-primary"
                  >
                    Restart Ollama
                  </Button>
                  <Button
                    variant="outline"
                    size="xs"
                    onClick={() => handleBulkRestart("docker")}
                    disabled={bulkLoading}
                    className="border-border-subtle text-xs text-text-primary"
                  >
                    Restart Docker
                  </Button>
                  <Button
                    variant="destructive"
                    size="xs"
                    onClick={handleBulkReboot}
                    disabled={bulkLoading}
                    className="gap-1 text-xs"
                  >
                    <RotateCcw className="h-3 w-3" aria-hidden="true" />
                    Reboot All
                  </Button>
                  <button
                    type="button"
                    onClick={() => setSelected(new Set())}
                    className="ml-auto text-xs text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:text-text-primary"
                  >
                    Clear
                  </button>
                </div>
              </motion.div>
            )}
          </AnimatePresence>

          <div className="mt-5">
            <MachineFleetToolbar
              search={search}
              onSearchChange={setSearch}
              onOpenCommandPalette={openCommandPalette}
              statusFilter={statusFilter}
              onStatusFilterChange={setStatusFilter}
              sortBy={sortBy}
              onSortChange={changeSort}
              tags={allTags}
              tagFilter={tagFilter}
              onTagFilterChange={setTagFilter}
              savedFilterCount={preferences.saved_filters.length}
              onApplySavedFilter={applySavedFilter}
              canSaveFilter={canControlFleet || preferences.saved_filters.length < 20}
              viewMode={viewMode}
              onViewModeChange={changeView}
              onArrange={() => setArrangeOpen(true)}
              arrangeDisabled={preferencesLoading || !hasReceivedData || machines.length < 2}
              resultCount={filteredMachines.length}
            />
          </div>

          <div className="mt-5">
            {viewMode === "list" ? (
              <MachineFleetTable
                machines={filteredMachines}
                selected={selected}
                onToggleSelect={toggleSelect}
                onToggleSelectAll={toggleSelectAll}
                canControlFleet={canControlFleet}
                canDeleteMachines={canDeleteMachines}
                canManageAPIMachines={canManageAPIMachines}
                onRefresh={canControlFleet ? refreshMachine : undefined}
                onEditAPIMachine={canManageAPIMachines ? openEditAPIMachine : undefined}
                onDelete={canDeleteMachines ? requestDelete : undefined}
                baselines={baselines}
                onToggleBaseline={toggleBaseline}
                noteOf={notes.note}
                onOpenNote={openNote}
                canEditNotes={canEditNotes}
              />
            ) : (
              <>
                <div
                  className="grid auto-rows-fr justify-start"
                  style={{
                    gap: "var(--grid-gap)",
                    // min(100%, …) clamps the column to the container at very
                    // narrow widths so a fixed 260–300px min never overflows.
                    gridTemplateColumns: "repeat(auto-fill, minmax(min(100%, var(--grid-min-col)), 360px))",
                  }}
                >
                  {!hasReceivedData && machines.length === 0 && !isDemo &&
                    Array.from({ length: 4 }).map((_, i) => (
                      <MachineCardSkeleton key={`skeleton-${i}`} />
                    ))}

                  {/* `layout` is real filter/sort state moving, not an entrance
                      flourish — there is no initial fade here. */}
                  <AnimatePresence mode="popLayout">
                    {filteredMachines.map((m) => (
                      <motion.div
                        key={m.machine_id}
                        layout
                        exit={{ opacity: 0 }}
                        transition={{ duration: 0.2 }}
                        className="h-full"
                      >
                        <MachineCard
                          machine={m}
                          expectedHighLoad={baselines.has(m.machine_id)}
                          onToggleExpectedHighLoad={toggleBaseline}
                          note={notes.note(m.machine_id)}
                          onOpenNote={openNote}
                          canEditNotes={canEditNotes}
                          onDelete={canDeleteMachines ? requestDelete : undefined}
                          onEdit={canManageAPIMachines ? openEditAPIMachine : undefined}
                          onRefresh={canControlFleet ? refreshMachine : undefined}
                        />
                      </motion.div>
                    ))}
                  </AnimatePresence>
                </div>

                {filteredMachines.length === 0 && hasReceivedData && (
                  <div className="flex flex-col items-center justify-center py-20 text-text-tertiary">
                    <Monitor className="mb-4 h-12 w-12 opacity-20" aria-hidden="true" />
                    <p className="text-sm">No machines match the current filters.</p>
                  </div>
                )}
              </>
            )}
          </div>
        </Disclosure>
      </section>

      {arrangeOpen && (
        <ArrangeMachinesDialog
          key={token}
          machines={orderMachines(machines, {
            sort: sortBy,
            order: preferences.machine_order,
            pinned: preferences.pinned_machines,
            status: m => STATUS_ORDER[getStatus(m, baselines)],
          })}
          onClose={() => setArrangeOpen(false)}
          onSave={saveMachineOrder}
        />
      )}

      <Dialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null); }}>
        <DialogContent className="border-border-subtle bg-surface-raised text-text-primary ring-0 sm:max-w-md" showCloseButton={false}>
          <DialogHeader>
            <div className="flex items-center gap-3">
              <div className="rounded-xl bg-status-critical-tint p-2">
                <Trash2 className="h-5 w-5 text-status-critical" aria-hidden="true" />
              </div>
              <DialogTitle className="text-text-primary">Delete Machine</DialogTitle>
            </div>
            <DialogDescription className="mt-2 text-xs text-text-tertiary">
              Are you sure you want to remove <span className="font-medium text-text-primary">{deleteTarget?.hostname}</span> from BloxOS? This will delete all historical data for this machine.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="border-t-border-subtle bg-transparent">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteTarget(null)}
              disabled={deleteLoading}
              className="border-border-subtle text-xs text-text-tertiary"
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

      {/* Notes, edited without leaving the Overview. Keyed on the machine so
          switching rows remounts the editor with that machine's text instead
          of copying props into state in an effect. */}
      {noteTarget && (
        <MachineNoteDialog
          key={noteTarget.id}
          machineID={noteTarget.id}
          hostname={noteTarget.hostname}
          note={notes.note(noteTarget.id)}
          canEdit={canEditNotes}
          onSave={notes.save}
          onReload={notes.reload}
          onClose={() => setNoteTarget(null)}
        />
      )}

      <AlertPanel
        open={alertPanelOpen}
        onClose={() => setAlertPanelOpen(false)}
        alerts={alerts}
        onAcknowledge={handleAcknowledge}
        onAcknowledgeAll={handleAcknowledgeAll}
      />

      {/* Add Machine / Add API machine / ⌘K are owned by AppShell now; only the
          edit flow, which needs a row's identity, stays with the page. */}
      {editAPIMachine && (
        <AddAPIMachineModal
          open={!!editAPIMachine}
          machine={editAPIMachine}
          onClose={() => setEditAPIMachine(null)}
          onSaved={handleAPIMachineSaved}
        />
      )}
    </>
  );
}

export default function Home() {
  // The route-derived page title already resolves to "Overview" (NAV_ITEMS),
  // so this page deliberately does not call usePageTitle().
  return (
    <AppShell>
      <OverviewContent />
    </AppShell>
  );
}
