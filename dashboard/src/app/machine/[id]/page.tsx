"use client";
import { AppShell } from "@/components/shell/AppShell";

// Machine detail.
//
// Monoform: this is the same product as the fleet view, with more of the data
// visible — the panels, tables, meters, status marks and controls are the ones
// /inventory and /versions use, at a finer grain. Deliberately NOT a separate
// "terminal" or "telemetry" aesthetic: live resource readings are table rows
// with a hairline meter, not gauges, and the terminal is a panel like any
// other. The shell owns the title (overridden here to the hostname), the rail
// and the global actions; the machine-scoped actions live in this page's lead.

import { useEffect, useState, useCallback, useRef, use, useMemo } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import dynamic from "next/dynamic";
import { useSSE } from "@/contexts/SSEContext";
import { MachineStatus } from "@/components/StatusBadge";
import { ServicePanel, Service } from "@/components/ServicePanel";
import { ContainerPanel, Container } from "@/components/ContainerPanel";
import { RebootModal } from "@/components/RebootModal";
import { MetricCharts } from "@/components/MetricCharts";
import { PowerHistory } from "@/components/PowerHistory";
import { HardwareCard, type HardwareInfo } from "@/components/HardwareCard";
import { MachineNotes } from "@/components/MachineNotes";
import { MachineMoreActions, type MoreAction } from "@/components/MachineMoreActions";
import { AISessionsPanel } from "@/components/AISessionsPanel";
import { useAISessions } from "@/contexts/AISessionsContext";
import { StatusCell, StatusMark, type MonoformTone } from "@/components/MonoformStatus";
import { latestGPUs } from "@/lib/gauge-data.mjs";
import { detailStatus, terminalStartError } from "@/lib/machine-status.mjs";
import { parseServerTimestamp } from "@/lib/timestamps";
import { usePageTitle } from "@/components/shell/PageTitle";
import {
  MF_BUTTON,
  MF_BUTTON_DANGER,
  MF_BUTTON_QUIET,
  MF_DIALOG,
  MF_INPUT,
  MF_PANEL_HEAD,
  MF_PANEL_TITLE,
  MF_TAB,
} from "@/lib/monoform-classes";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import {
  Terminal as TerminalIcon, RotateCcw,
  Lock, Unlock, X, Maximize2, Minimize2, Trash2,
  LayoutDashboard, Box, Container as ContainerIcon, StickyNote, KeyRound,
  RefreshCw, Copy, Check, Bot, BarChart3, AlertTriangle,
} from "lucide-react";
import { HUB_URL, getAuthHeaders } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";

type DetailTab = "overview" | "services" | "containers" | "metrics" | "ai-sessions" | "notes" | "terminal";
const DETAIL_TABS: readonly DetailTab[] = ["overview", "services", "containers", "metrics", "ai-sessions", "notes", "terminal"] as const;
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

function getStatus(data: MachineData, now: number): MachineStatus {
  const maxGpuTempC = Math.max(
    data.metrics?.gpu_temp ?? 0,
    ...(data.gpus ?? []).map((g) => g.temp_c ?? 0),
  );
  const diskPct = (data.metrics?.disk_total_bytes ?? 0) > 0
    ? ((data.metrics?.disk_used_bytes ?? 0) / data.metrics.disk_total_bytes) * 100 : 0;
  // Freshness is judged from the last heartbeat, never from the REST
  // snapshot fetched at page open (finding: an open detail page kept
  // calling a quiet agent "live"). Shares the fleet's thresholds.
  return detailStatus({
    apiStatus: data.machine.status,
    lastSeenMs: parseServerTimestamp(data.machine.last_seen),
    now,
    maxGpuTempC,
    diskPct,
  });
}

/* ---- Shared readings ------------------------------------------------------
 * One rule for what counts as a warning, used by the resource table and the
 * graphics table so a 91% disk and a 91% GPU never disagree. */

function loadTone(pct: number): MonoformTone {
  if (pct >= 90) return "critical";
  if (pct >= 75) return "warning";
  return "ok";
}

function tempTone(celsius: number): MonoformTone {
  if (celsius >= 80) return "critical";
  if (celsius >= 60) return "warning";
  return "ok";
}

const TONE_LABEL: Record<MonoformTone, string> = {
  ok: "Nominal",
  warning: "Elevated",
  critical: "Critical",
  stale: "Stale",
  neutral: "Not reported",
};

const STATUS_TONE: Record<MachineStatus, MonoformTone> = {
  live: "ok",
  warning: "warning",
  critical: "critical",
  offline: "critical",
  stale: "stale",
};

/** The hairline load meter used in every reading row. */
function Meter({
  pct,
  variant,
  className = "w-32",
}: {
  pct: number;
  variant?: "gpu" | "warning";
  /** The meter is decoration for the value beside it, so its width belongs to
   *  the layout that hosts it rather than to the meter. */
  className?: string;
}) {
  const clamped = Math.max(0, Math.min(100, pct));
  return (
    <span
      className={`mf-meter ${className} ${variant === "gpu" ? "mf-meter--gpu" : ""} ${variant === "warning" ? "mf-meter--warning" : ""}`}
      aria-hidden
    >
      <i style={{ width: `${clamped}%` }} />
    </span>
  );
}

/** One row of the resource table: name, value, meter, state. */
function ReadingRow({
  label,
  value,
  detail,
  pct,
  tone,
  gpu,
}: {
  label: string;
  value: string;
  detail?: string;
  pct?: number;
  tone?: MonoformTone;
  gpu?: boolean;
}) {
  return (
    <tr>
      <td className="text-[13px] text-text-primary">
        {label}
        {detail && <span className="ml-2 font-mono text-[11px] text-text-tertiary">{detail}</span>}
      </td>
      <td className="mf-metric text-[13px] text-text-primary">{value}</td>
      <td>
        {pct === undefined ? (
          <span className="text-text-disabled">—</span>
        ) : (
          <Meter
            pct={pct}
            variant={tone === "warning" || tone === "critical" ? "warning" : gpu ? "gpu" : undefined}
          />
        )}
      </td>
      <td>
        {tone ? <StatusCell tone={tone} label={TONE_LABEL[tone]} /> : <span className="text-text-disabled">—</span>}
      </td>
    </tr>
  );
}

export default function MachineDetailPage({ params }: { params: Promise<{ id: string }> }) {
  return <AppShell><MachineDetailContent params={params} /></AppShell>;
}

function MachineDetailContent({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  const { getMachine } = useSSE();
  const [baseData, setBaseData] = useState<MachineData | null>(null);
  const [services, setServices] = useState<Service[]>([]);
  const [containers, setContainers] = useState<Container[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const [showReboot, setShowReboot] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [showRevokeConfirm, setShowRevokeConfirm] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);
  const [showReenrollDialog, setShowReenrollDialog] = useState(false);
  const [preparingReenroll, setPreparingReenroll] = useState(false);
  const [reenrollError, setReenrollError] = useState<string | null>(null);
  const [reenrollResponse, setReenrollResponse] = useState<{
    machine_id: string;
    windows_command: string;
    ca_url: string;
    ca_sha256: string;
    expires_at: string;
  } | null>(null);
  const [reenrollCopied, setReenrollCopied] = useState<"command" | "sha" | null>(null);
  const { hasScope, authFetch } = useAuth();
  const aiSessions = useAISessions();
  const aiSessionCount = aiSessions.enabled === false ? 0 : (aiSessions.getMachine(id)?.sessions.length ?? 0);
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
  const [termError, setTermError] = useState<string | null>(null);
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

  // Liveness — derived at render time, no useMemo needed. The `now` state
  // is updated by a 1Hz interval (below), so this re-evaluates each tick
  // without tripping React 19's idempotence rule on Date.now().
  const lastSeenMs = sseData?.last_seen ?? 0;
  const isLive = lastSeenMs > 0 && now - lastSeenMs < 120_000;

  const data = useMemo<MachineData | null>(() => {
    if (!baseData) return null;
    if (!sseData) return baseData;

    return {
      ...baseData,
      gpus: latestGPUs(sseData.gpus, baseData.gpus),
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

  // The top bar's route-derived title for /machine/* is the generic
  // {title: "Machine", kicker: "Fleet"}; the hostname is the only name an
  // operator actually navigates by, so it replaces it as soon as it loads.
  usePageTitle(data?.machine.hostname, "Machine");

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const handlePinSubmit = useCallback(async () => {
    setPinError(false);
    setTermError(null);
    setTermState("connecting");
    const hdrs = authHeaders();
    hdrs["Content-Type"] = "application/json";
    try {
      const res = await fetch(`${HUB_URL}/api/machines/${id}/terminal`, {
        method: "POST",
        headers: hdrs,
        body: JSON.stringify({ pin: pinInput }),
      });
      if (res.status === 403) {
        setPinError(true);
        setPinInput("");
        setTermState("pin_entry");
        pinInputRef.current?.focus();
        return;
      }
      if (!res.ok) {
        // Surface the hub's reason (rate limit, agent offline, ...) instead
        // of a bare disconnect — the user needs to know what to do next.
        let hubError = "";
        try {
          hubError = (await res.json())?.error ?? "";
        } catch { /* non-JSON body */ }
        setTermError(terminalStartError(res.status, hubError));
        setTermState("disconnected");
        return;
      }
      const d = await res.json();
      setPinInput("");
      setTermSessionId(d.session_id);
      setTermBrowserToken(d.browser_token);
      setTermState("active");
    } catch {
      setTermError("Terminal request was interrupted — check the hub connection and retry.");
      setTermState("disconnected");
    }
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
    setDeleteError(null);
    try {
      const res = await fetch(`${HUB_URL}/api/machines/${id}`, {
        method: "DELETE",
        headers: authHeaders(),
      });
      if (res.ok) {
        router.push("/");
        return;
      }
      // Failure: surface the hub's reason and keep the dialog open so the
      // delete can be retried — never close silently.
      let hubError = "";
      try {
        hubError = (await res.json())?.error ?? "";
      } catch { /* non-JSON body */ }
      setDeleteError(hubError ? `Delete failed: ${hubError}` : `Delete failed (HTTP ${res.status}).`);
    } catch {
      setDeleteError("Delete failed: request interrupted. The machine may still exist — retry or refresh to check.");
    } finally {
      setDeleting(false);
    }
  }, [id, router]);

  const handleRevokeCredential = useCallback(async () => {
    setRevoking(true);
    setRevokeError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/machines/${id}/credential`, {
        method: "DELETE",
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setRevokeError(data?.error || `Failed to revoke credential (${res.status})`);
        return;
      }
      setShowRevokeConfirm(false);
    } catch {
      setRevokeError("Failed to revoke credential. Is the hub reachable?");
    } finally {
      setRevoking(false);
    }
  }, [authFetch, id]);

  // Preparing a re-enrollment command is inert: it mints a machine-bound
  // token and returns a command, but does not touch the current credential
  // or connection. Only running the returned command on the host performs
  // the actual credential rotation.
  const handlePrepareReenrollment = useCallback(async () => {
    setPreparingReenroll(true);
    setReenrollError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/machines/${id}/windows-re-enrollment`, {
        method: "POST",
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setReenrollError(data?.error || `Failed to prepare re-enrollment (${res.status})`);
        return;
      }
      setReenrollResponse(data);
    } catch {
      setReenrollError("Failed to prepare re-enrollment. Is the hub reachable?");
    } finally {
      setPreparingReenroll(false);
    }
  }, [authFetch, id]);

  const handleCopyReenroll = useCallback(async (text: string, which: "command" | "sha") => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setReenrollCopied(which);
      setTimeout(() => setReenrollCopied(null), 2000);
    } catch {
      // Clipboard denied (permissions/non-secure context) — never claim a
      // copy that did not happen.
      setReenrollError("Copy failed — select and copy the text manually.");
    }
  }, []);

  useEffect(() => {
    if (termState === "pin_entry") {
      setTimeout(() => pinInputRef.current?.focus(), 100);
    }
  }, [termState]);

  if (error && !data) {
    return (
      <div className="mf-panel px-6 py-10 text-center">
        <p className="text-[13px] text-status-critical">{error}</p>
        <Link href="/" className="mt-4 inline-block text-[13px] text-accent hover:underline">
          Back to the fleet
        </Link>
      </div>
    );
  }

  if (!data) {
    return (
      <div className="flex items-center gap-2.5 py-20 text-[13px] text-text-tertiary">
        <span className="w-3.5 h-3.5 border-2 border-accent/40 border-t-accent rounded-full animate-spin" />
        Loading…
      </div>
    );
  }

  const { machine, metrics } = data;
  const status = getStatus(data, now);
  const cpuPct = metrics?.cpu_percent ?? 0;
  const ramPct = (metrics?.ram_total_bytes ?? 0) > 0 ? ((metrics?.ram_used_bytes ?? 0) / metrics.ram_total_bytes) * 100 : 0;
  const diskPct = (metrics?.disk_total_bytes ?? 0) > 0 ? ((metrics?.disk_used_bytes ?? 0) / metrics.disk_total_bytes) * 100 : 0;
  const gpus = data.gpus || [];
  const hasGpu = gpus.length > 0;
  // Controls follow the same freshness policy as the badge: a stale or
  // offline machine does not accept commands.
  const isOnline = status === "live" || status === "warning";
  const sseM = getMachine(id);
  const machineTags = sseM?.tags ? sseM.tags.split(",").map((t: string) => t.trim().toLowerCase()) : [];
  const isAPIMachine = machineTags.includes("synology") || machineTags.includes("proxmox");

  // The overflow menu's contents. Each gate is written out here, beside the
  // dialog it opens, rather than inside the menu component — the menu is a
  // renderer, and a permission check hidden one file away is a permission
  // check nobody reviews. Every onSelect body is the click handler these
  // actions already had; the dialogs themselves are untouched.
  const moreActions: MoreAction[] = [];
  if (canDelete && !isAPIMachine) {
    moreActions.push({
      key: "revoke",
      label: "Revoke credential",
      Icon: KeyRound,
      onSelect: () => {
        setRevokeError(null);
        setShowRevokeConfirm(true);
      },
    });
  }
  if (canDelete && !isAPIMachine && (machine.os?.toLowerCase().includes("windows") ?? false)) {
    moreActions.push({
      key: "reenroll",
      label: "Prepare Windows re-enrollment",
      Icon: RefreshCw,
      onSelect: () => {
        setReenrollError(null);
        setReenrollResponse(null);
        setShowReenrollDialog(true);
      },
    });
  }
  if (canDelete) {
    moreActions.push({
      key: "delete",
      label: "Delete machine",
      Icon: Trash2,
      destructive: true,
      onSelect: () => {
        setDeleteError(null);
        setShowDeleteConfirm(true);
      },
    });
  }

  // The lead's Terminal button goes to the tab and, on a machine that can
  // answer, advances the same locked -> pin_entry step the tab's own Unlock
  // button performs. It is never disabled: the tab is worth reading offline
  // (it shows the locked state and why), so hiding the way in would be worse
  // than letting the PIN form explain itself.
  const openTerminalTab = () => {
    setActiveTab("terminal");
    if (termState === "locked" && isOnline) setTermState("pin_entry");
  };

  return (
    <>
      {/* Delete dialog */}
      <Dialog open={showDeleteConfirm} onOpenChange={(o) => { if (!o) setShowDeleteConfirm(false); }}>
        <DialogContent className={`${MF_DIALOG} sm:max-w-md`} showCloseButton={false}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2.5">
              <Trash2 className="w-4 h-4 text-status-critical" aria-hidden />
              Delete machine
            </DialogTitle>
            <DialogDescription className="mt-2 text-[13px] leading-6 text-text-tertiary">
              Remove <span className="font-medium text-text-primary">{machine.hostname}</span> from
              BloxOS? All of its historical data is deleted with it.
            </DialogDescription>
            {deleteError && (
              <p role="alert" className="mt-2 text-xs text-status-critical">{deleteError}</p>
            )}
          </DialogHeader>
          <DialogFooter>
            <button
              type="button"
              onClick={() => setShowDeleteConfirm(false)}
              disabled={deleting}
              className={MF_BUTTON_QUIET}
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleDeleteMachine}
              disabled={deleting}
              className={MF_BUTTON_DANGER}
            >
              {deleting ? "Deleting…" : "Delete machine"}
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Credential revoke dialog */}
      <Dialog
        open={showRevokeConfirm}
        onOpenChange={(open) => {
          if (!open && !revoking) {
            setShowRevokeConfirm(false);
            setRevokeError(null);
          }
        }}
      >
        <DialogContent className={`${MF_DIALOG} sm:max-w-md`} showCloseButton={false}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2.5">
              <KeyRound className="w-4 h-4 text-status-warning" aria-hidden />
              Revoke enrollment credential
            </DialogTitle>
            <DialogDescription className="mt-2 text-[13px] leading-6 text-text-tertiary">
              This immediately disconnects{" "}
              <span className="font-medium text-text-primary">{machine.hostname}</span>. Its machine
              record and history are preserved, but it stays offline until you run a fresh Add Machine
              command on that host.
            </DialogDescription>
          </DialogHeader>
          {revokeError && (
            <p
              role="alert"
              className="rounded-lg border border-status-critical/40 bg-status-critical-tint px-3 py-2 text-[11px] text-status-critical"
            >
              {revokeError}
            </p>
          )}
          <DialogFooter>
            <button
              type="button"
              onClick={() => {
                setShowRevokeConfirm(false);
                setRevokeError(null);
              }}
              disabled={revoking}
              className={MF_BUTTON_QUIET}
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleRevokeCredential}
              disabled={revoking}
              className={MF_BUTTON_DANGER}
            >
              {revoking ? "Revoking…" : "Revoke credential"}
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Windows re-enrollment dialog */}
      <Dialog
        open={showReenrollDialog}
        onOpenChange={(open) => {
          if (!open && !preparingReenroll) {
            setShowReenrollDialog(false);
            setReenrollError(null);
            setReenrollResponse(null);
          }
        }}
      >
        <DialogContent className={`${MF_DIALOG} sm:max-w-lg`} showCloseButton={false}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2.5">
              <RefreshCw className="w-4 h-4 text-accent" aria-hidden />
              Prepare Windows re-enrollment
            </DialogTitle>
            <DialogDescription className="mt-2 text-[13px] leading-6 text-text-tertiary">
              {reenrollResponse ? (
                <>
                  Run this command on{" "}
                  <span className="font-medium text-text-primary">{machine.hostname}</span> in an
                  elevated PowerShell session. It expires at{" "}
                  <span className="font-medium text-text-primary">
                    {new Date(reenrollResponse.expires_at).toLocaleString()}
                  </span>
                  . Running it rotates the credential; nothing changes on the hub until then.
                </>
              ) : (
                <>
                  Preparing the command does not disconnect{" "}
                  <span className="font-medium text-text-primary">{machine.hostname}</span> — it stays
                  connected and authenticated until you run the returned command on that host, which is
                  what actually performs the credential rotation.
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          {reenrollError && (
            <p
              role="alert"
              className="rounded-lg border border-status-critical/40 bg-status-critical-tint px-3 py-2 text-[11px] text-status-critical"
            >
              {reenrollError}
            </p>
          )}
          {reenrollResponse && (
            <div className="space-y-3">
              <div className="relative">
                <pre className="max-h-48 overflow-y-auto rounded-lg border border-border-default bg-surface-sunken p-3 pr-24 font-mono text-[11px] leading-5 text-text-primary whitespace-pre-wrap break-all">
                  {reenrollResponse.windows_command}
                </pre>
                <button
                  type="button"
                  onClick={() => handleCopyReenroll(reenrollResponse.windows_command, "command")}
                  className={`${MF_BUTTON} absolute top-2 right-2 h-8`}
                  aria-label="Copy re-enrollment command"
                >
                  {reenrollCopied === "command" ? (
                    <Check className="w-3 h-3" aria-hidden />
                  ) : (
                    <Copy className="w-3 h-3" aria-hidden />
                  )}
                  Copy
                </button>
              </div>
              {reenrollResponse.ca_sha256 && (
                <div className="relative">
                  <div className="mf-kicker mb-1">CA fingerprint (SHA-256)</div>
                  <code className="block rounded-lg border border-border-default bg-surface-sunken p-2.5 pr-28 font-mono text-[10px] text-text-primary break-all">
                    {reenrollResponse.ca_sha256}
                  </code>
                  <button
                    type="button"
                    onClick={() => handleCopyReenroll(reenrollResponse.ca_sha256, "sha")}
                    className={`${MF_BUTTON} absolute top-5 right-2 h-8`}
                    aria-label="Copy CA SHA-256 fingerprint"
                  >
                    {reenrollCopied === "sha" ? (
                      <Check className="w-3 h-3" aria-hidden />
                    ) : (
                      <Copy className="w-3 h-3" aria-hidden />
                    )}
                    Copy SHA
                  </button>
                </div>
              )}
            </div>
          )}
          <DialogFooter>
            <button
              type="button"
              onClick={() => {
                setShowReenrollDialog(false);
                setReenrollError(null);
                setReenrollResponse(null);
              }}
              disabled={preparingReenroll}
              className={MF_BUTTON_QUIET}
            >
              {reenrollResponse ? "Close" : "Cancel"}
            </button>
            {!reenrollResponse && (
              <button
                type="button"
                onClick={handlePrepareReenrollment}
                disabled={preparingReenroll}
                className="mf-action"
              >
                {preparingReenroll ? "Preparing…" : "Prepare command"}
              </button>
            )}
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

      {/* The machine lead: what is true right now, the facts that identify it,
          and what can safely be done to it.

          No hostname here. The shell's top bar already renders it as the
          page's <h1> (usePageTitle above), and a second 28px copy of the same
          word 60px underneath would be two headings for one machine.

          No rack, slot or placement either: the machines table has hostname,
          ip, os, status, tags, last_seen, hardware_info and notes, and nothing
          that says where the box physically is. A lead that showed "Rack 01 ·
          slot 01" would be furniture, not data.

          Terminal is the one filled action because it is the one an operator
          reaches for; Reboot stays outlined beside it; recovery and deletion
          move into More with their gates and dialogs untouched. */}
      <section className="mf-machine-lead" aria-label="Machine status and actions">
        <div className="mf-machine-state-line">
          <StatusMark tone={STATUS_TONE[status]} label={status} className="capitalize" />
          {isAPIMachine && (
            <span className="mf-suppressed" title="Polled over an API rather than running a BloxOS agent">
              API-polled
            </span>
          )}
        </div>

        <div className="mf-machine-lead-side">
          <dl className="mf-machine-facts">
            <Fact
              stack
              mono
              label="Last report"
              value={effectiveLastUpdated ? timeSince(effectiveLastUpdated) : "—"}
            />
            {(data.latency_ms ?? 0) > 0 && (
              <Fact stack mono label="Latency" value={`${data.latency_ms} ms`} />
            )}
            {machine.ip && <Fact stack mono label="IP address" value={machine.ip} />}
            {machine.os && <Fact stack label="Platform" value={machine.os} />}
            <Fact stack mono small label="ID" value={machine.id} />
          </dl>

          <div className="mf-machine-actions">
            {!isAPIMachine && canControl && (
              <button
                type="button"
                onClick={() => setShowReboot(true)}
                disabled={!isOnline}
                className={MF_BUTTON}
                title={isOnline ? "Reboot this machine" : "Machine is not reporting — reboot unavailable"}
              >
                <RotateCcw className="w-3.5 h-3.5" aria-hidden />
                Reboot
              </button>
            )}
            <MachineMoreActions items={moreActions} />
            {!isAPIMachine && canControl && (
              <button
                type="button"
                onClick={openTerminalTab}
                className="mf-action"
                title={
                  isOnline
                    ? "Open the remote terminal"
                    : "Machine is not reporting — the terminal cannot connect"
                }
              >
                <TerminalIcon className="w-3.5 h-3.5" aria-hidden />
                {termState === "active" ? "Terminal" : "Open terminal"}
              </button>
            )}
          </div>
        </div>
      </section>

      <Tabs value={activeTab} onValueChange={(v) => setActiveTab(v as DetailTab)}>
        {/* The rail scrolls sideways rather than wrapping: seven tabs on a
            narrow screen would otherwise become two noisy rows, and the active
            underline would sit above the row it belongs to. */}
        <div className="mf-tab-rail">
        <TabsList variant="line" className="gap-1">
          <TabsTrigger value="overview" className={MF_TAB}>
            <LayoutDashboard className="w-4 h-4" aria-hidden />
            Overview
          </TabsTrigger>
          <TabsTrigger value="services" className={MF_TAB}>
            <Box className="w-4 h-4" aria-hidden />
            Services
            {/* A count only when there is something to count — "Services 0" is
                not a finding, it is noise on every machine without units. */}
            {liveServices.length > 0 && (
              <span className="ml-1 font-mono text-[10px] text-text-tertiary">{liveServices.length}</span>
            )}
          </TabsTrigger>
          <TabsTrigger value="containers" className={MF_TAB}>
            <ContainerIcon className="w-4 h-4" aria-hidden />
            Containers
            {liveContainers.length > 0 && (
              <span className="ml-1 font-mono text-[10px] text-text-tertiary">{liveContainers.length}</span>
            )}
          </TabsTrigger>
          <TabsTrigger value="metrics" className={MF_TAB}>
            <BarChart3 className="w-4 h-4" aria-hidden />
            Metrics
          </TabsTrigger>
          {!isAPIMachine && (
            <TabsTrigger value="ai-sessions" className={MF_TAB}>
              <Bot className="w-4 h-4" aria-hidden />
              AI Sessions
              {aiSessionCount > 0 && (
                <span className="ml-1 font-mono text-[10px] text-text-tertiary">{aiSessionCount}</span>
              )}
            </TabsTrigger>
          )}
          <TabsTrigger value="notes" className={MF_TAB}>
            <StickyNote className="w-4 h-4" aria-hidden />
            Notes
          </TabsTrigger>
          {!isAPIMachine && canControl && (
            <TabsTrigger value="terminal" className={MF_TAB}>
              <TerminalIcon className="w-4 h-4" aria-hidden />
              Terminal
              {termState === "active" && (
                <span className="ml-1 font-mono text-[10px] text-status-ok">on</span>
              )}
            </TabsTrigger>
          )}
        </TabsList>
        </div>

        {/* Overview — what is true right now, then what this machine is.
            Live readings and Graphics sit side by side for inspection; the
            hardware profile is a quiet full-width strip underneath. */}
        <TabsContent value="overview" className="mt-7">
          {/* data-gpu rather than :has() — the column count is a fact the page
              already knows, and a machine with no GPU gives its readings the
              full width instead of an empty panel beside them. */}
          <div className="mf-machine-overview-grid" data-gpu={hasGpu ? "true" : "false"}>
          <section className="mf-panel mf-machine-readings overflow-hidden">
            <div className={MF_PANEL_HEAD}>
              <h2 className={MF_PANEL_TITLE}>Live readings</h2>
              <span className="mf-kicker">
                {effectiveLastUpdated ? `reported ${timeSince(effectiveLastUpdated)}` : "no report yet"}
              </span>
            </div>
            <div className="mf-table-wrap overflow-x-auto">
              <table className="mf-table">
                <thead>
                  <tr>
                    <th>Resource</th>
                    <th>Value</th>
                    <th>Load</th>
                    <th>State</th>
                  </tr>
                </thead>
                <tbody>
                  <ReadingRow
                    label="CPU"
                    detail={data.hardware_info?.cpu_model}
                    value={`${cpuPct.toFixed(1)}%`}
                    pct={cpuPct}
                    tone={loadTone(cpuPct)}
                  />
                  {(metrics?.cpu_temp_c ?? 0) > 0 && (
                    <ReadingRow
                      label="CPU temperature"
                      value={`${(metrics.cpu_temp_c ?? 0).toFixed(0)}°C`}
                      tone={tempTone(metrics.cpu_temp_c ?? 0)}
                    />
                  )}
                  <ReadingRow
                    label="Memory"
                    detail={`${formatBytes(metrics?.ram_used_bytes)} / ${formatBytes(metrics?.ram_total_bytes)}`}
                    value={`${ramPct.toFixed(0)}%`}
                    pct={ramPct}
                    tone={loadTone(ramPct)}
                  />
                  <ReadingRow
                    label="Disk"
                    detail={`${formatBytes(metrics?.disk_used_bytes)} / ${formatBytes(metrics?.disk_total_bytes)}`}
                    value={`${diskPct.toFixed(0)}%`}
                    pct={diskPct}
                    tone={loadTone(diskPct)}
                  />
                  {(data.latency_ms ?? 0) > 0 && (
                    <ReadingRow label="Network latency" value={`${data.latency_ms}ms`} />
                  )}
                </tbody>
              </table>
            </div>
          </section>

          {hasGpu && (
            <section className="mf-panel mf-machine-graphics overflow-hidden">
              <div className={MF_PANEL_HEAD}>
                <h2 className={MF_PANEL_TITLE}>Graphics</h2>
                <span className="mf-kicker">
                  {gpus.length} device{gpus.length === 1 ? "" : "s"}
                </span>
              </div>
              {/* One section per device rather than six columns across. Power
                  and fan appear only where the device reports them — the
                  alternative, a permanent column of em dashes, spends the same
                  width on the absence of data. Nothing is hidden behind a
                  disclosure: a number worth showing is worth showing. */}
              <div>
                {gpus.map((gpu) => {
                  const util = gpu.util_percent ?? 0;
                  const temp = gpu.temp_c ?? 0;
                  const vramPct = (gpu.mem_total_bytes ?? 0) > 0
                    ? ((gpu.mem_used_bytes ?? 0) / gpu.mem_total_bytes) * 100
                    : 0;
                  const tTone = tempTone(temp);
                  return (
                    <div className="mf-gpu-row" key={gpu.index}>
                      <div className="mf-gpu-head">
                        <span className="min-w-0 truncate">
                          <span className="mf-metric text-[11px] text-text-tertiary">GPU {gpu.index}</span>
                          <span className="ml-2.5 text-[13px] font-semibold text-text-primary">
                            {gpu.name || "GPU"}
                          </span>
                        </span>
                        <span className="mf-gpu-util">
                          <span className="mf-metric text-[13px] text-text-primary">{util.toFixed(0)}%</span>
                          <Meter pct={util} variant="gpu" className="w-16" />
                        </span>
                      </div>
                      <dl className="mf-gpu-meta">
                        <GpuStat label="VRAM">
                          {(gpu.mem_total_bytes ?? 0) > 0 ? (
                            <>
                              {formatBytes(gpu.mem_used_bytes)}
                              <span className="ml-1.5 text-[11px] text-text-tertiary">
                                / {formatBytes(gpu.mem_total_bytes)}
                              </span>
                              <Meter pct={vramPct} variant="gpu" className="mt-1.5 w-full" />
                            </>
                          ) : (
                            <span className="text-text-disabled">—</span>
                          )}
                        </GpuStat>
                        <GpuStat label="Temperature">
                          {tTone === "ok" ? (
                            `${temp}°C`
                          ) : (
                            <StatusCell tone={tTone} label={`${temp}°C`} Icon={AlertTriangle} />
                          )}
                        </GpuStat>
                        {(gpu.power_watts ?? 0) > 0 && (
                          <GpuStat label="Power">{`${(gpu.power_watts ?? 0).toFixed(0)} W`}</GpuStat>
                        )}
                        {(gpu.fan_percent ?? 0) > 0 && (
                          <GpuStat label="Fan">{`${(gpu.fan_percent ?? 0).toFixed(0)}%`}</GpuStat>
                        )}
                      </dl>
                    </div>
                  );
                })}
              </div>
            </section>
          )}
          </div>

          {data.hardware_info && (
            <div className="mf-machine-hardware">
              <HardwareCard hw={data.hardware_info} />
            </div>
          )}
        </TabsContent>

        <TabsContent value="services" className="mt-7">
          <ServicePanel services={liveServices} machineId={id} hubUrl={HUB_URL} />
        </TabsContent>

        <TabsContent value="containers" className="mt-7">
          <ContainerPanel containers={liveContainers} machineId={id} hubUrl={HUB_URL} />
        </TabsContent>

        <TabsContent value="metrics" className="mt-7">
          {/* No outer panel: it existed only because PowerHistory was a
              border-top continuation rather than a surface of its own. It is a
              panel now, so the charts are no longer panels inside a panel. */}
          <div className="mf-machine-metrics">
            <MetricCharts machineId={id} hasGpu={hasGpu} />
            {!isAPIMachine && <PowerHistory key={id} machineId={id} />}
          </div>
        </TabsContent>

        {!isAPIMachine && (
          <TabsContent value="ai-sessions" className="mt-7">
            <AISessionsPanel machineId={id} />
          </TabsContent>
        )}

        <TabsContent value="notes" className="mt-7">
          {/* PHASE12-NOTE: keying on `notes ?? ""` remounts the
              component when the server-fetched notes change (e.g.
              after a refresh) so MachineNotes can avoid the React 19
              set-state-in-effect anti-pattern. */}
          <MachineNotes
            key={machine.notes ?? ""}
            machineId={id}
            initialNotes={machine.notes ?? ""}
          />
        </TabsContent>

        {/* Terminal tab — keepMounted so the PTY WebSocket survives tab switches. */}
        {!isAPIMachine && canControl && (
          <TabsContent value="terminal" keepMounted className="mt-7 data-[hidden]:hidden">
            <section className="mf-panel overflow-hidden">
              <div className={MF_PANEL_HEAD}>
                <div className="flex items-center gap-3">
                  <h2 className={MF_PANEL_TITLE}>Terminal</h2>
                  {termState === "active" && <StatusCell tone="ok" label="connected" />}
                  {termState === "connecting" && (
                    <span className="font-mono text-[11px] text-accent">connecting…</span>
                  )}
                  {termState === "disconnected" && (
                    <StatusCell tone="critical" label="disconnected" />
                  )}
                </div>
                <div className="flex items-center gap-1">
                  {termState === "active" && (
                    <>
                      <button
                        type="button"
                        onClick={() => setTermExpanded(!termExpanded)}
                        className="grid h-8 w-8 place-items-center rounded-lg text-text-tertiary transition-colors hover:bg-surface-elevated hover:text-text-primary"
                        title={termExpanded ? "Collapse terminal" : "Expand terminal"}
                        aria-label={termExpanded ? "Collapse terminal" : "Expand terminal"}
                      >
                        {termExpanded ? (
                          <Minimize2 className="w-3.5 h-3.5" />
                        ) : (
                          <Maximize2 className="w-3.5 h-3.5" />
                        )}
                      </button>
                      <button
                        type="button"
                        onClick={handleTerminalClose}
                        className="grid h-8 w-8 place-items-center rounded-lg text-text-tertiary transition-colors hover:bg-surface-elevated hover:text-status-critical"
                        title="Close terminal"
                        aria-label="Close terminal"
                      >
                        <X className="w-3.5 h-3.5" />
                      </button>
                    </>
                  )}
                  {termState === "disconnected" && (
                    <button
                      type="button"
                      onClick={() => {
                        setTermError(null);
                        setTermState("pin_entry");
                      }}
                      className={MF_BUTTON_QUIET}
                    >
                      Reconnect
                    </button>
                  )}
                </div>
              </div>

              {/* Stable-height body container — prevents pane height jumping between states */}
              <div
                className="bg-surface-sunken transition-all duration-[var(--motion-base)]"
                style={{
                  minHeight: termState === "active" && termExpanded ? 600 : 360,
                  height: termState === "active" ? (termExpanded ? 600 : 360) : "auto",
                }}
              >
                {termState === "locked" && (
                  <div className="flex h-[360px] flex-col items-center justify-center gap-3">
                    <Lock className="h-8 w-8 text-text-disabled" aria-hidden />
                    <div className="text-center">
                      <p className="text-[13px] text-text-primary">Remote terminal</p>
                      <p className="mt-1 text-[11px] text-text-tertiary">
                        Enter the PIN to unlock terminal access
                      </p>
                    </div>
                    <button
                      type="button"
                      onClick={() => isOnline && setTermState("pin_entry")}
                      disabled={!isOnline}
                      className={`${MF_BUTTON} mt-2`}
                      title={isOnline ? undefined : "Machine is not reporting — terminal unavailable"}
                    >
                      <Unlock className="w-3.5 h-3.5" aria-hidden />
                      Unlock terminal
                    </button>
                  </div>
                )}

                {termState === "pin_entry" && (
                  <div className="flex h-[360px] flex-col items-center justify-center gap-3">
                    <Lock className="h-7 w-7 text-accent" aria-hidden />
                    <p className="text-[13px] text-text-primary">Enter PIN to open terminal</p>
                    <form
                      onSubmit={(e) => {
                        e.preventDefault();
                        handlePinSubmit();
                      }}
                      className="mt-1 flex items-center gap-2"
                    >
                      <Input
                        ref={pinInputRef}
                        type="password"
                        value={pinInput}
                        onChange={(e) => {
                          setPinInput(e.target.value);
                          setPinError(false);
                        }}
                        placeholder="PIN"
                        aria-label="Terminal PIN"
                        aria-invalid={pinError || undefined}
                        className={`${MF_INPUT} w-32 text-center font-mono ${pinError ? "border-status-critical" : ""}`}
                        autoComplete="off"
                      />
                      <button type="submit" className="mf-action">
                        Open
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          setTermState("locked");
                          setPinInput("");
                          setPinError(false);
                        }}
                        className={MF_BUTTON_QUIET}
                      >
                        Cancel
                      </button>
                    </form>
                    {pinError && (
                      <p role="alert" className="mt-1 text-xs text-status-critical">
                        Invalid PIN
                      </p>
                    )}
                  </div>
                )}

                {termState === "connecting" && (
                  <div className="flex h-[360px] flex-col items-center justify-center gap-3">
                    <span className="h-6 w-6 rounded-full border-2 border-accent border-t-transparent animate-spin" />
                    <p className="text-[13px] text-text-tertiary">Starting terminal session…</p>
                  </div>
                )}

                {termState === "active" && termSessionId && (
                  <div
                    className="h-full"
                    style={{
                      height: termExpanded ? 600 : 360,
                      // Match the xterm background exactly so there's no
                      // seam between xterm's canvas and the wrapper.
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
                  <div className="flex h-[360px] flex-col items-center justify-center gap-3">
                    <TerminalIcon className="h-7 w-7 text-status-critical" aria-hidden />
                    <p className="text-[13px] text-text-tertiary">Terminal disconnected</p>
                    {termError && (
                      <p role="alert" className="max-w-sm text-center text-xs text-status-critical">
                        {termError}
                      </p>
                    )}
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={() => setTermState("pin_entry")}
                        className={MF_BUTTON}
                      >
                        Reconnect
                      </button>
                      <button
                        type="button"
                        onClick={() => {
                          setTermState("locked");
                          setTermSessionId(null);
                        }}
                        className={MF_BUTTON_QUIET}
                      >
                        Close
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </section>
          </TabsContent>
        )}
      </Tabs>
    </>
  );
}

/** One labelled GPU value. Label above, value below, mono. */
function GpuStat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="mf-kicker uppercase">{label}</dt>
      <dd className="mf-metric mt-1 text-[13px] text-text-primary">{children}</dd>
    </div>
  );
}

function Fact({
  label,
  value,
  mono,
  small,
  stack,
}: {
  label: string;
  value: string;
  mono?: boolean;
  small?: boolean;
  /** Label above value, left-aligned — the lead's column-of-facts form. */
  stack?: boolean;
}) {
  return (
    <div className={stack ? "flex flex-col items-start gap-1" : "flex items-baseline justify-between gap-3"}>
      <dt className="mf-kicker">{label}</dt>
      <dd
        className={`min-w-0 truncate ${stack ? "" : "text-right"} ${
          mono ? "mf-metric" : ""
        } ${small ? "text-[11px] text-text-tertiary" : "text-[13px] text-text-secondary"}`}
        title={value}
      >
        {value}
      </dd>
    </div>
  );
}
