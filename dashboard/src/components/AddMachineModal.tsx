"use client";

import { useState, useCallback } from "react";
import { Copy, Check, Terminal, ArrowLeft } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { HUB_URL, getStoredToken } from "@/lib/session";

interface AddMachineModalProps {
  open: boolean;
  onClose: () => void;
}

interface TokenResponse {
  token: string;
  expires_at: string; // RFC3339
  command: string; // server-built Linux paste-block (env vars + curl bootstrap)
  ca_url?: string;
  ca_sha256?: string;
}

type OSChoice = "linux" | "windows";

type ModalState =
  | { kind: "choose-os" }
  | { kind: "loading"; os: OSChoice }
  | { kind: "show-command"; os: OSChoice; resp: TokenResponse };

function deriveHttpHubBase(): string {
  const base =
    HUB_URL ||
    (typeof window !== "undefined" ? window.location.origin : "");
  return base.replace(/^wss:/, "https:").replace(/^ws:/, "http:");
}

function deriveWsHubBase(): string {
  const base =
    HUB_URL ||
    (typeof window !== "undefined" ? window.location.origin : "");
  return base.replace(/^https:/, "wss:").replace(/^http:/, "ws:");
}

function buildWindowsCommand(token: string): string {
  const wsBase = deriveWsHubBase();
  const httpBase = deriveHttpHubBase();
  // Tls12 + ServerCertificateValidationCallback shim makes the bootstrap IWR
  // tolerate Caddy's self-signed cert on PowerShell 5.1 (the default on
  // Windows 10/11). install.ps1 itself installs a TrustAllCerts shim for the
  // subsequent agent fetches. -SkipCertificateCheck is PS 7+ only and would
  // break on stock Windows installs.
  return [
    `$env:BLOXOS_HUB="${wsBase}"`,
    `$env:BLOXOS_TOKEN="${token}"`,
    `[System.Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12`,
    `[System.Net.ServicePointManager]::ServerCertificateValidationCallback = {$true}`,
    `iwr -UseBasicParsing ${httpBase}/install.ps1 | iex`,
  ].join("; ");
}

export function AddMachineModal({ open, onClose }: AddMachineModalProps) {
  const [state, setState] = useState<ModalState>({ kind: "choose-os" });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const handleClose = useCallback(() => {
    setState({ kind: "choose-os" });
    setError(null);
    setCopied(false);
    onClose();
  }, [onClose]);

  const chooseOS = useCallback(async (os: OSChoice) => {
    setState({ kind: "loading", os });
    setError(null);
    try {
      const authToken = getStoredToken();
      const headers: Record<string, string> = {};
      if (authToken) headers["Authorization"] = `Bearer ${authToken}`;
      const res = await fetch(`${HUB_URL}/api/tokens`, {
        method: "POST",
        headers,
      });
      if (!res.ok) {
        setError("Failed to generate token. Is the hub reachable?");
        setState({ kind: "choose-os" });
        return;
      }
      const data: TokenResponse = await res.json();
      setState({ kind: "show-command", os, resp: data });
    } catch {
      setError("Failed to generate token. Is the hub reachable?");
      setState({ kind: "choose-os" });
    }
  }, []);

  const goBack = useCallback(() => {
    setState({ kind: "choose-os" });
    setError(null);
    setCopied(false);
  }, []);

  const handleCopy = useCallback((text: string) => {
    if (!text) return;
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, []);

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) handleClose();
      }}
    >
      <DialogContent
        className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-2xl"
        showCloseButton
      >
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-blue/10">
              <Terminal className="w-4 h-4 text-blox-blue" />
            </div>
            <DialogTitle className="text-blox-text">Add Machine</DialogTitle>
          </div>
          <DialogDescription className="text-blox-muted text-xs leading-relaxed pt-1">
            {state.kind === "show-command"
              ? "Run the command below on the target machine. Token expires in 15 minutes."
              : "What OS is this machine? We'll generate the right install command for you."}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {error && state.kind === "choose-os" && (
            <div className="text-[11px] text-blox-red bg-blox-red/10 border border-blox-red/30 rounded-lg px-3 py-2">
              {error}
            </div>
          )}

          {state.kind === "choose-os" && <OSPicker onChoose={chooseOS} />}

          {state.kind === "loading" && (
            <div className="py-12 flex flex-col items-center gap-3">
              <div className="w-6 h-6 border-2 border-blox-blue/30 border-t-blox-blue rounded-full animate-spin" />
              <p className="text-xs text-blox-muted">
                Generating install token…
              </p>
            </div>
          )}

          {state.kind === "show-command" && (
            <CommandDisplay
              os={state.os}
              resp={state.resp}
              copied={copied}
              onCopy={handleCopy}
              onBack={goBack}
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/* ─── OS Picker ────────────────────────────────────────────────── */

function OSPicker({ onChoose }: { onChoose: (os: OSChoice) => void }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <OSCard
        emoji="🐧"
        label="Linux"
        subtitle="Ubuntu, Debian, Proxmox VM, or any modern Linux distro"
        onClick={() => onChoose("linux")}
      />
      <OSCard
        emoji="🪟"
        label="Windows"
        subtitle="Windows 10, 11, or Server (2019+). Requires Administrator PowerShell"
        onClick={() => onChoose("windows")}
      />
    </div>
  );
}

function OSCard({
  emoji,
  label,
  subtitle,
  onClick,
}: {
  emoji: string;
  label: string;
  subtitle: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="group text-left p-5 rounded-xl border border-blox-border bg-blox-card hover:border-blox-blue/40 hover:bg-blox-blue/5 transition-all"
    >
      <div className="flex items-start gap-3">
        <span className="text-3xl leading-none mt-0.5">{emoji}</span>
        <div className="flex-1 min-w-0">
          <h3 className="text-sm font-semibold text-blox-text group-hover:text-blox-blue transition-colors">
            {label}
          </h3>
          <p className="text-[11px] text-blox-muted mt-1 leading-snug">
            {subtitle}
          </p>
        </div>
      </div>
    </button>
  );
}

/* ─── Command Display ──────────────────────────────────────────── */

function CommandDisplay({
  os,
  resp,
  copied,
  onCopy,
  onBack,
}: {
  os: OSChoice;
  resp: TokenResponse;
  copied: boolean;
  onCopy: (text: string) => void;
  onBack: () => void;
}) {
  const command =
    os === "linux" ? resp.command : buildWindowsCommand(resp.token);
  const osLabel = os === "linux" ? "Linux" : "Windows";
  const osEmoji = os === "linux" ? "🐧" : "🪟";
  const helpText =
    os === "linux"
      ? "Downloads the agent, creates a systemd service, and starts reporting metrics."
      : "Run this in PowerShell as Administrator. Registers BloxOSAgent as a Windows service.";
  const cmdLabel =
    os === "linux"
      ? "Run as root on Linux"
      : "Run in elevated PowerShell on Windows";

  return (
    <>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="text-xl leading-none">{osEmoji}</span>
          <span className="text-sm font-medium text-blox-text">
            {osLabel} install command
          </span>
        </div>
        <button
          type="button"
          onClick={onBack}
          className="flex items-center gap-1 text-[11px] text-blox-muted hover:text-blox-blue transition-colors"
        >
          <ArrowLeft className="w-3 h-3" />
          Choose different OS
        </button>
      </div>

      <div>
        <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
          {cmdLabel}
        </label>
        <div className="relative">
          <pre className="bg-blox-bg border border-blox-border rounded-xl p-3 pr-24 text-[10px] font-mono text-blox-text whitespace-pre-wrap break-all overflow-x-auto leading-relaxed">
            {command}
          </pre>
          <Button
            size="sm"
            onClick={() => onCopy(command)}
            className="absolute top-2 right-2 bg-blox-blue text-white hover:bg-blox-blue/90 text-xs gap-1.5"
            aria-label="Copy install command"
          >
            {copied ? (
              <>
                <Check className="w-3 h-3" />
                Copied
              </>
            ) : (
              <>
                <Copy className="w-3 h-3" />
                Copy
              </>
            )}
          </Button>
        </div>
        <p className="text-[10px] text-blox-muted mt-2 leading-relaxed">
          {helpText} Token is valid until{" "}
          <span className="font-mono">{resp.expires_at}</span>.
        </p>
      </div>
    </>
  );
}
