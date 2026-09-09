"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw, DownloadCloud, TerminalSquare, AlertTriangle, CheckCircle2, Loader2 } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { canRequestUpdate, isActiveState, pollPhase, statusLabel } from "@/lib/update-status.mjs";

const POLL_MS = 2500;

interface UpdateStatus {
  request_id?: string;
  state?: string;
  version?: string;
  message?: string;
  updated_at?: string;
}

interface SystemUpdateResponse {
  available: boolean;
  reason?: string;
  cli_command?: string;
  current_version?: string;
  mode?: string;
  status?: UpdateStatus | null;
}

interface UpdatePhase {
  phase: string;
  state: string | null;
  message: string;
}

// Settings → Updates: request and watch a host-worker update. The button only
// ever POSTs {"target_version":"latest"}; progress comes from polling
// GET /api/system/update and tolerates the expected short hub downtime.
export function UpdatesSettings() {
  const { authFetch } = useAuth();
  const [info, setInfo] = useState<SystemUpdateResponse | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [phase, setPhase] = useState<UpdatePhase>({ phase: "idle", state: null, message: "" });
  const [requestError, setRequestError] = useState<string | null>(null);
  const [requesting, setRequesting] = useState(false);
  const phaseRef = useRef(phase);
  phaseRef.current = phase;
  const requestIDRef = useRef<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopPolling = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const pollOnce = useCallback(async () => {
    let resp: SystemUpdateResponse | null = null;
    let failed = false;
    try {
      const res = await authFetch("/api/system/update");
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      resp = await res.json();
      setInfo(resp);
    } catch {
      failed = true;
    }
    const next = pollPhase(phaseRef.current, resp, failed, requestIDRef.current);
    phaseRef.current = next;
    setPhase(next);
    if (next.phase === "succeeded" || next.phase === "failed" ||
        next.phase === "rolled_back" || next.phase === "unavailable") {
      stopPolling();
    }
  }, [authFetch, stopPolling]);

  const startPolling = useCallback(() => {
    if (timerRef.current) return;
    timerRef.current = setInterval(() => { void pollOnce(); }, POLL_MS);
  }, [pollOnce]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch("/api/system/update");
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const resp = await res.json();
        if (cancelled) return;
        setInfo(resp);
        setPhase(pollPhase(null, resp, false));
        if (resp.status && isActiveState(resp.status.state)) startPolling();
      } catch (err) {
        if (!cancelled) setLoadError(err instanceof Error ? err.message : "request failed");
      }
    })();
    return () => { cancelled = true; stopPolling(); };
  }, [authFetch, startPolling, stopPolling]);

  const requestUpdate = useCallback(async () => {
    setRequesting(true);
    setRequestError(null);
    try {
      const res = await authFetch("/api/system/update", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ target_version: "latest" }),
      });
      const data = await res.json().catch(() => ({}));
      if (res.status === 202) {
        if (typeof data.request_id !== "string" || !data.request_id) throw new Error("Missing request identity");
        requestIDRef.current = data.request_id;
        const accepted = { phase: "active", state: "checking", message: data.message || "update requested" };
        phaseRef.current = accepted;
        setPhase(accepted);
        startPolling();
      } else {
        setRequestError(data.error || `request failed (HTTP ${res.status})`);
      }
    } catch {
      setRequestError("Could not confirm the request. Refresh status before trying again.");
      const unknown = { phase: "unreachable", state: null, message: "Request outcome is not confirmed" };
      phaseRef.current = unknown;
      setPhase(unknown);
      stopPolling();
    } finally {
      setRequesting(false);
    }
  }, [authFetch, startPolling, stopPolling]);

  if (loadError) {
    return (
      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <p className="text-sm text-red-400">Could not load update status: {loadError}</p>
      </section>
    );
  }
  if (!info) {
    return (
      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <p className="text-sm text-blox-muted">Loading update status…</p>
      </section>
    );
  }

  const busy = requesting || phase.phase === "active" || phase.phase === "accepted" || phase.phase === "reconnecting";
  const canRequest = canRequestUpdate(info) && !busy;

  return (
    <section className="rounded-xl border border-blox-border bg-blox-card p-5 space-y-4" aria-label="System updates">
      <div className="flex items-start justify-between gap-3 flex-wrap">
        <div>
          <h2 className="text-sm font-semibold text-blox-text">System updates</h2>
          <p className="text-xs text-blox-muted mt-1">
            The host worker downloads both components and backs up before installing; expect a short dashboard
            outage while the new build starts. The current build is{" "}
            <span className="font-mono text-blox-text">{info.current_version}</span>.
          </p>
        </div>
        <Button
          size="sm"
          onClick={requestUpdate}
          disabled={!canRequest}
          className="bg-blox-blue text-white hover:bg-blox-blue/90 text-xs gap-1.5"
        >
          {busy ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <DownloadCloud className="w-3.5 h-3.5" />}
          Update BloxOS
        </Button>
      </div>

      {info.available !== true ? (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 space-y-2">
          <p className="text-xs text-blox-text font-medium flex items-center gap-1.5">
            <AlertTriangle className="w-3.5 h-3.5 text-amber-400" />
            One-time setup required
          </p>
          <p className="text-xs text-blox-muted">
            {info.reason || "The updater is not configured on this hub."} Run this
            on the hub host as root, then refresh:
          </p>
          <code className="block text-xs font-mono bg-blox-bg border border-blox-border rounded px-2 py-1.5 text-blox-text w-fit">
            <TerminalSquare className="w-3 h-3 inline mr-1.5 -mt-0.5" />
            {info.cli_command || "sudo bloxos-update init"}
          </code>
          <a
            className="text-xs text-blox-blue hover:underline"
            href="https://github.com/bokiko/bloxos/blob/main/docs/system-updates.md#enable-updates-on-an-older-installation"
            target="_blank"
            rel="noreferrer"
          >
            One-time setup guide (required on older installations) →
          </a>
        </div>
      ) : (
        <div className="space-y-2">
          <p className="text-xs text-blox-muted">
            Updater mode: <span className="text-blox-text font-medium">
              {info.mode === "native" ? "Native (systemd)" : info.mode === "compose" ? "Docker Compose" : info.mode}
            </span>
          </p>
          <div className="flex items-center gap-2 text-xs">
            {phase.phase === "succeeded" ? (
              <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
            ) : phase.phase === "failed" ? (
              <AlertTriangle className="w-3.5 h-3.5 text-red-400" />
            ) : busy ? (
              <Loader2 className="w-3.5 h-3.5 text-blox-blue animate-spin" />
            ) : null}
            <span className="text-blox-text font-medium">
              {phase.phase === "reconnecting"
                ? "Reconnecting — hub is restarting (expected during update)"
                : phase.phase === "unreachable"
                  ? "Status temporarily unreachable"
                  : phase.phase === "accepted"
                    ? "Request accepted — waiting for the worker to start"
                    : statusLabel(phase.state)}
            </span>
            {phase.message ? <span className="text-blox-muted">— {phase.message}</span> : null}
          </div>
          {requestError && (
            <p className="text-xs text-red-400">{requestError}</p>
          )}
        </div>
      )}

      {info.available === true && (
        <p className="text-xs text-blox-muted">
          From the server terminal: <code className="font-mono text-blox-text">sudo bloxos-update update</code>
        </p>
      )}

      {info.available === true && !busy && (
        <Button variant="ghost" size="sm" onClick={() => void pollOnce()} className="text-xs text-blox-muted hover:text-blox-text gap-1.5 h-8">
          <RefreshCw className="w-3.5 h-3.5" />
          Refresh status
        </Button>
      )}
    </section>
  );
}
