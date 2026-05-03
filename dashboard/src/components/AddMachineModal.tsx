"use client";

import { useState, useCallback, useMemo } from "react";
import { Copy, Check, Terminal } from "lucide-react";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { HUB_URL, getStoredToken } from "@/lib/session";

interface AddMachineModalProps {
  open: boolean;
  onClose: () => void;
}

type OSTab = "linux" | "windows";

interface TokenResponse {
  token: string;
  expires_at: string; // RFC3339; informational only on this UI
  command: string; // Linux paste-block, already includes BLOXOS_CA_URL/SHA when needed
  ca_url?: string;
  ca_sha256?: string;
}

// PHASE12-NOTE: the spec called the token endpoint `/api/install-tokens`,
// the actual hub route is `/api/tokens` (handleCreateToken). Spec also
// names a non-existent `expires_in` (seconds) field — the response is
// `expires_at` RFC3339. We use the real shape.
//
// PHASE12-NOTE: there is no `hub_url` field on the response. The Linux
// `command` already embeds the hub URL via `export BLOXOS_HUB=...`. For
// the Windows tab we derive the http hub base from `getHubWsBaseUrl()`'s
// http analogue (HUB_URL || window.location.origin), which matches what
// the Linux command embeds (publicAndWebsocketBase in the hub).
function deriveHttpHubBase(): string {
  const base =
    HUB_URL ||
    (typeof window !== "undefined" ? window.location.origin : "");
  // Normalize wss:///ws:// -> https/http for IWR.
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
  // Bootstrap with curl.exe (ships on Win10 1803+ and all Win11) instead
  // of Invoke-WebRequest. IWR sits on .NET HttpWebRequest, which fails
  // against self-signed Caddy certs over TLS 1.3 — Schannel mislabels the
  // TLS 1.3 NewSessionTicket frame as renegotiation and HttpWebRequest
  // bails with "underlying connection was closed". curl.exe uses Schannel
  // for TLS but its own HTTP stack and handles both cleanly.
  //
  // The downloaded install.ps1 is then run via `powershell.exe
  // -ExecutionPolicy Bypass -File`. Stock Windows PowerShell 5.1 has
  // ExecutionPolicy=Restricted by default (all scopes Undefined → falls
  // back to Restricted), which blocks running .ps1 files from disk. The
  // Bypass flag scopes ONLY to that single child PowerShell process — it
  // does NOT modify the system, user, or process-scope policy persistently.
  // Same pattern used by winget, scoop, oh-my-posh.
  //
  // Why this works under Restricted: execution policy gates loading .ps1
  // files from disk. It does NOT gate (a) inline `-Command "..."` strings,
  // (b) string `iex`/Invoke-Expression of in-memory content, (c) built-in
  // cmdlets, or (d) `Add-Type` C# compilation. install.ps1 itself uses
  // only built-in cmdlets and Win32 binaries (sc.exe, curl.exe, the agent
  // .exe), so no nested .ps1 invocations exist that would need the same
  // bypass treatment.
  return [
    `$env:BLOXOS_HUB="${wsBase}"`,
    `$env:BLOXOS_TOKEN="${token}"`,
    `curl.exe -ksfL -o $env:TEMP\\bloxos-install.ps1 ${httpBase}/install.ps1`,
    `powershell.exe -ExecutionPolicy Bypass -File $env:TEMP\\bloxos-install.ps1`,
  ].join("; ");
}

export function AddMachineModal({ open, onClose }: AddMachineModalProps) {
  const [resp, setResp] = useState<TokenResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [tab, setTab] = useState<OSTab>("linux");
  const [copiedTab, setCopiedTab] = useState<OSTab | null>(null);

  const generateToken = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const authToken = getStoredToken();
      const headers: Record<string, string> = {};
      if (authToken) headers["Authorization"] = `Bearer ${authToken}`;
      const res = await fetch(`${HUB_URL}/api/tokens`, { method: "POST", headers });
      if (!res.ok) {
        setResp(null);
        setError("Failed to generate token. Is the hub reachable?");
        return;
      }
      const data: TokenResponse = await res.json();
      setResp(data);
    } catch {
      setResp(null);
      setError("Failed to generate token. Is the hub reachable?");
    } finally {
      setLoading(false);
    }
  }, []);

  const windowsCommand = useMemo(
    () => (resp ? buildWindowsCommand(resp.token) : ""),
    [resp]
  );

  const copy = useCallback(
    (which: OSTab, text: string) => {
      if (!text) return;
      navigator.clipboard.writeText(text);
      setCopiedTab(which);
      setTimeout(
        () => setCopiedTab((current) => (current === which ? null : current)),
        2000
      );
    },
    []
  );

  const handleClose = useCallback(() => {
    setResp(null);
    setError(null);
    setCopiedTab(null);
    setTab("linux");
    onClose();
  }, [onClose]);

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) handleClose(); }}>
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-2xl" showCloseButton>
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-blue/10">
              <Terminal className="w-4 h-4 text-blox-blue" />
            </div>
            <DialogTitle className="text-blox-text">Add Machine</DialogTitle>
          </div>
        </DialogHeader>

        <div className="space-y-4">
          {!resp ? (
            <>
              <DialogDescription className="text-blox-muted text-xs leading-relaxed">
                Generate a one-time install token, then run the matching command
                on the target machine. The same token works for either OS.
                Tokens expire in 15 minutes.
              </DialogDescription>
              {error && (
                <div className="text-[11px] text-blox-red bg-blox-red/10 border border-blox-red/30 rounded-lg px-3 py-2">
                  {error}
                </div>
              )}
              <Button
                onClick={generateToken}
                disabled={loading}
                variant="outline"
                className="w-full text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 text-xs"
              >
                {loading ? "Generating..." : "Generate Install Token"}
              </Button>
            </>
          ) : (
            <>
              <Tabs value={tab} onValueChange={(v) => setTab(v as OSTab)}>
                <TabsList variant="line" className="gap-1">
                  <TabsTrigger value="linux" className="px-4 py-1.5 text-sm">
                    Linux
                  </TabsTrigger>
                  <TabsTrigger value="windows" className="px-4 py-1.5 text-sm">
                    Windows
                  </TabsTrigger>
                </TabsList>

                <TabsContent value="linux" className="mt-4">
                  <CommandBlock
                    label="Run as root on Linux"
                    command={resp.command}
                    onCopy={() => copy("linux", resp.command)}
                    copied={copiedTab === "linux"}
                  />
                  <p className="text-[10px] text-blox-muted mt-2 leading-relaxed">
                    Downloads the agent, creates a systemd service, and starts
                    reporting metrics. Token is valid until{" "}
                    <span className="font-mono">{resp.expires_at}</span>.
                  </p>
                </TabsContent>

                <TabsContent value="windows" className="mt-4">
                  <CommandBlock
                    label="Run in elevated PowerShell on Windows"
                    command={windowsCommand}
                    onCopy={() => copy("windows", windowsCommand)}
                    copied={copiedTab === "windows"}
                  />
                  <p className="text-[10px] text-blox-muted mt-2 leading-relaxed">
                    Downloads the agent to C:\Program Files\BloxOS, registers
                    the BloxOSAgent Windows service, and starts it. Token is
                    valid until{" "}
                    <span className="font-mono">{resp.expires_at}</span>.
                  </p>
                </TabsContent>
              </Tabs>

              <Button
                onClick={handleClose}
                variant="outline"
                className="w-full text-xs border-blox-border text-blox-text"
              >
                Done
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface CommandBlockProps {
  label: string;
  command: string;
  copied: boolean;
  onCopy: () => void;
}

function CommandBlock({ label, command, copied, onCopy }: CommandBlockProps) {
  return (
    <div>
      <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
        {label}
      </label>
      <div className="relative">
        <pre className="text-[10px] text-blox-text bg-blox-bg border border-blox-border rounded-xl p-3 pr-10 overflow-x-auto font-mono leading-relaxed whitespace-pre-wrap break-all">
          {command}
        </pre>
        <button
          onClick={onCopy}
          className="absolute top-2 right-2 p-1.5 rounded-lg hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors"
          title="Copy to clipboard"
          aria-label="Copy install command"
        >
          {copied ? (
            <Check className="w-3.5 h-3.5 text-emerald-400" />
          ) : (
            <Copy className="w-3.5 h-3.5" />
          )}
        </button>
      </div>
    </div>
  );
}
