"use client";

import { useState, useCallback, useEffect, useRef } from "react";
import { Copy, Check, Terminal, ArrowLeft, ChevronRight, AlertTriangle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { installTokenMinutesRemaining } from "@/lib/install-token-expiry.mjs";

interface AddMachineModalProps {
  open: boolean;
  onClose: () => void;
}

interface TokenResponse {
  token: string;
  expires_at: string; // RFC3339
  /** Linux: the short one-line join command (older hubs: the full bootstrap). */
  command: string;
  windows_command: string;
  ca_url: string;
  ca_sha256: string;
  /** The complete Linux bootstrap the short command fetches. Absent on older hubs. */
  advanced_command?: string;
  /** The link the short command fetches. Never shown; the command carries it. */
  join_url?: string;
  /** SPKI SHA-256 the short command pins behind a private CA. Absent otherwise. */
  join_pin?: string;
}

type OSChoice = "linux" | "windows";

type ModalState =
  | { kind: "choose-os" }
  | { kind: "loading"; os: OSChoice }
  | { kind: "show-command"; os: OSChoice; resp: TokenResponse };

/** Which text was last copied, so each Copy button reports only its own success. */
type CopyTarget = "command" | "advanced" | "sha" | null;

export function AddMachineModal({ open, onClose }: AddMachineModalProps) {
  const [state, setState] = useState<ModalState>({ kind: "choose-os" });
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<CopyTarget>(null);
  const [copyError, setCopyError] = useState<string | null>(null);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const resetCopy = useCallback(() => {
    if (copyTimer.current) clearTimeout(copyTimer.current);
    copyTimer.current = null;
    setCopied(null);
    setCopyError(null);
  }, []);

  useEffect(() => resetCopy, [resetCopy]);

  const handleClose = useCallback(() => {
    setState({ kind: "choose-os" });
    setError(null);
    resetCopy();
    onClose();
  }, [onClose, resetCopy]);

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
        const data = await res.json().catch(() => null);
        setError(data?.error || `Failed to generate token (${res.status})`);
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
    resetCopy();
  }, [resetCopy]);

  const handleCopy = useCallback(
    async (text: string, target: Exclude<CopyTarget, null>) => {
      if (!text) return;
      if (copyTimer.current) clearTimeout(copyTimer.current);
      try {
        await navigator.clipboard.writeText(text);
        setCopyError(null);
        setCopied(target);
        copyTimer.current = setTimeout(() => setCopied(null), 2000);
      } catch {
        setCopied(null);
        setCopyError("Couldn't copy automatically. Select the text and copy it yourself.");
      }
    },
    []
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) handleClose();
      }}
    >
      <DialogContent
        className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-2xl max-h-[calc(100dvh-2rem)] overflow-y-auto"
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
              ? state.os === "linux"
                ? "One command on the new machine, and it joins this hub."
                : "Run the command below on the new machine as Administrator."
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
                Generating install command…
              </p>
            </div>
          )}

          {state.kind === "show-command" && (
            <CommandDisplay
              os={state.os}
              resp={state.resp}
              copied={copied}
              copyError={copyError}
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

/* ─── Expiry ───────────────────────────────────────────────────── */

/**
 * Human wording for the token expiry, re-evaluated each minute so the
 * "about N minutes" stays true while the dialog sits open.
 */
function useExpiry(expiresAt: string) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(id);
  }, []);
  const at = new Date(expiresAt);
  const valid = !Number.isNaN(at.getTime());
  const remainingMs = valid ? at.getTime() - now : 0;
  const minutes = installTokenMinutesRemaining(remainingMs);
  const sameDay = valid && at.toDateString() === new Date(now).toDateString();
  const localTime = !valid
    ? expiresAt
    : sameDay
      ? at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
      : at.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  return { valid, expired: valid && remainingMs <= 0, minutes, localTime, iso: valid ? at.toISOString() : expiresAt };
}

function ExpiryNote({ expiresAt, onBack }: { expiresAt: string; onBack: () => void }) {
  const exp = useExpiry(expiresAt);
  if (!exp.valid) {
    // No parseable expiry from the hub: state the policy rather than a blank.
    return (
      <p className="text-[11px] text-blox-muted leading-relaxed">
        Commands stop working about 15 minutes after they are generated. Come back here for a fresh one.
      </p>
    );
  }
  if (exp.expired) {
    return (
      <p className="text-[11px] text-blox-amber leading-relaxed" role="status">
        This command has expired.{" "}
        <button type="button" onClick={onBack} className="underline underline-offset-2 hover:text-blox-text">
          Generate a new one
        </button>
        .
      </p>
    );
  }
  return (
    <p className="text-[11px] text-blox-muted leading-relaxed">
      Works until{" "}
      <time dateTime={exp.iso} className="text-blox-text font-medium">
        {exp.localTime}
      </time>
      {exp.valid && (
        <>
          {" "}
          ({exp.minutes <= 1 ? "less than a minute" : `about ${exp.minutes} minutes`} from now)
        </>
      )}
      . After that, come back here for a fresh one.
    </p>
  );
}

/* ─── Shared pieces ────────────────────────────────────────────── */

function CopyButton({
  text,
  target,
  copied,
  onCopy,
  label,
  ariaLabel,
  primary,
  className,
}: {
  text: string;
  target: Exclude<CopyTarget, null>;
  copied: CopyTarget;
  onCopy: (text: string, target: Exclude<CopyTarget, null>) => void;
  label: string;
  ariaLabel: string;
  primary?: boolean;
  className?: string;
}) {
  const done = copied === target;
  return (
    <Button
      size="sm"
      variant={primary ? "default" : "outline"}
      onClick={() => onCopy(text, target)}
      className={
        (primary
          ? "bg-blox-blue text-white hover:bg-blox-blue/90 "
          : "border-blox-border ") +
        "text-xs gap-1.5 shrink-0 " +
        (className ?? "")
      }
      aria-label={ariaLabel}
    >
      {done ? (
        <>
          <Check className="w-3 h-3" />
          Copied
        </>
      ) : (
        <>
          <Copy className="w-3 h-3" />
          {label}
        </>
      )}
    </Button>
  );
}

/** A copyable block of monospace text with its own Copy button. */
function CodeBlock({
  text,
  label,
  copyLabel,
  ariaLabel,
  target,
  copied,
  onCopy,
  primary,
  maxHeight,
}: {
  text: string;
  label?: string;
  copyLabel: string;
  ariaLabel: string;
  target: Exclude<CopyTarget, null>;
  copied: CopyTarget;
  onCopy: (text: string, target: Exclude<CopyTarget, null>) => void;
  primary?: boolean;
  maxHeight?: string;
}) {
  return (
    <div>
      {label && (
        <div className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 font-medium">{label}</div>
      )}
      <div className="relative">
        <pre
          className={
            "bg-blox-bg border border-blox-border rounded-xl p-3 pr-24 font-mono text-blox-text whitespace-pre-wrap break-all overflow-x-auto leading-relaxed " +
            (primary ? "text-[11px] sm:text-xs " : "text-[10px] ") +
            (maxHeight ? `${maxHeight} overflow-y-auto` : "")
          }
          tabIndex={0}
        >
          {text}
        </pre>
        <CopyButton
          text={text}
          target={target}
          copied={copied}
          onCopy={onCopy}
          label={copyLabel}
          ariaLabel={ariaLabel}
          primary={primary}
          className="absolute top-2 right-2"
        />
      </div>
    </div>
  );
}

/** Collapsed-by-default disclosure. Native details/summary: keyboard and screen-reader accessible as is. */

function Disclosure({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <details className="group rounded-xl border border-blox-border bg-blox-bg/60">
      <summary className="flex items-center gap-2 cursor-pointer select-none px-3 py-2.5 text-[11px] font-medium text-blox-muted hover:text-blox-text rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-blox-blue/50 list-none [&::-webkit-details-marker]:hidden">
        <ChevronRight className="w-3.5 h-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
        {title}
      </summary>
      <div className="px-3 pb-3 pt-1 space-y-3 border-t border-blox-border/60">{children}</div>
    </details>
  );
}

function CAFingerprint({
  sha,
  copied,
  onCopy,
}: {
  sha: string;
  copied: CopyTarget;
  onCopy: (text: string, target: Exclude<CopyTarget, null>) => void;
}) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-3">
        <p className="text-[10px] uppercase tracking-wider font-medium text-blox-muted">Hub CA SHA-256</p>
        <CopyButton
          text={sha}
          target="sha"
          copied={copied}
          onCopy={onCopy}
          label="Copy SHA"
          ariaLabel="Copy CA SHA-256 fingerprint"
        />
      </div>
      <code className="block text-[10px] font-mono text-blox-text break-all">{sha}</code>
      <p className="text-[10px] text-blox-muted leading-relaxed">
        This fingerprint came through your authenticated dashboard session and is embedded in the command. For a
        cold-start or higher-assurance setup, compare it through a separate trusted channel before running the
        command. That out-of-band check is offered, not required for the normal authenticated-dashboard flow.
      </p>
    </div>
  );
}

/* ─── Command Display ──────────────────────────────────────────── */

/**
 * The bootstrap's transport, taken from what the hub itself returned — the
 * join URL when present, otherwise the URL embedded in the command text for
 * older hubs. The dashboard's own origin is deliberately never used to guess:
 * the hub may be reached by the new machine over a different scheme than the
 * operator's browser uses.
 */
function bootstrapTransport(resp: TokenResponse, os: OSChoice): "http" | "https" | null {
  const text =
    os === "windows" ? resp.windows_command : (resp.join_url ?? resp.command);
  const match = text.match(/https?:\/\//);
  if (!match) return null;
  return match[0] === "http://" ? "http" : "https";
}

/** Visible, no interaction required: shown before the command whenever the hub is plaintext. */
function HttpBootstrapWarning() {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-lg border border-blox-amber/40 bg-blox-amber/10 px-3 py-2 text-[11px] text-blox-amber leading-relaxed"
    >
      <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
      <span>
        This hub uses unencrypted HTTP. Only use this command on an isolated test network; use HTTPS for other
        installations.
      </span>
    </div>
  );
}

function CommandDisplay({
  os,
  resp,
  copied,
  copyError,
  onCopy,
  onBack,
}: {
  os: OSChoice;
  resp: TokenResponse;
  copied: CopyTarget;
  copyError: string | null;
  onCopy: (text: string, target: Exclude<CopyTarget, null>) => void;
  onBack: () => void;
}) {
  const header = (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-2">
        <span className="text-xl leading-none">{os === "linux" ? "🐧" : "🪟"}</span>
        <span className="text-sm font-medium text-blox-text">
          {os === "linux" ? "Linux" : "Windows"} install command
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
  );

  const copyErrorNote = copyError && (
    <p className="text-[11px] text-blox-amber" role="alert">
      {copyError}
    </p>
  );

  const transport = bootstrapTransport(resp, os);

  if (os === "windows") {
    return (
      <>
        {header}
        {transport === "http" && <HttpBootstrapWarning />}
        <CodeBlock
          text={resp.windows_command}
          label="Run in elevated PowerShell on Windows"
          copyLabel="Copy"
          ariaLabel="Copy install command"
          target="command"
          copied={copied}
          onCopy={onCopy}
          primary
          maxHeight="max-h-64"
        />
        {copyErrorNote}
        <p className="text-[11px] text-blox-muted leading-relaxed">
          Run this in PowerShell as Administrator. Registers BloxOSAgent as a Windows service.
        </p>
        <ExpiryNote expiresAt={resp.expires_at} onBack={onBack} />
        {resp.ca_sha256 && (
          <Disclosure title="Certificate details">
            <CAFingerprint sha={resp.ca_sha256} copied={copied} onCopy={onCopy} />
          </Disclosure>
        )}
      </>
    );
  }

  // Linux. An older hub returns the full bootstrap as `command` and no
  // advanced_command; the primary view still shows whatever is runnable, and
  // the disclosure only appears when it has something to say.
  const advanced = resp.advanced_command && resp.advanced_command !== resp.command ? resp.advanced_command : "";
  const hasAdvanced = Boolean(advanced || resp.ca_sha256 || resp.join_pin);

  return (
    <>
      {header}
      {transport === "http" && <HttpBootstrapWarning />}
      <CodeBlock
        text={resp.command}
        copyLabel="Copy"
        ariaLabel="Copy install command"
        target="command"
        copied={copied}
        onCopy={onCopy}
        primary
      />
      {copyErrorNote}
      <div className="space-y-1.5">
        <p className="text-xs text-blox-text leading-relaxed">
          Paste this into a Terminal on the new machine and press Enter. It installs the BloxOS agent and connects
          it to this hub automatically.
        </p>
        <p className="text-[11px] text-blox-muted leading-relaxed">
          You may be asked for your password once so it can install the service.
        </p>
        <ExpiryNote expiresAt={resp.expires_at} onBack={onBack} />
      </div>

      {hasAdvanced && (
        <Disclosure title="Advanced & troubleshooting">
          <div className="space-y-1.5 text-[11px] text-blox-muted leading-relaxed">
            <p>
              The command downloads a small install script from this hub and runs it once. Nothing runs if the
              download fails.
            </p>
            {resp.join_pin && (
              <p>
                <span className="text-blox-text">Secure connection. </span>The command pins this hub&apos;s current
                TLS key (<code className="font-mono text-[10px] break-all">{resp.join_pin}</code>), so a different
                server can&apos;t answer in its place. Hub certificates renew periodically; if the machine prints{" "}
                <code className="font-mono text-[10px]">curl: (90) SSL: public key does not match pinned public key</code>
                , the key changed since this command was made. Come back here and generate a new one.
              </p>
            )}
            <p>
              If the machine prints{" "}
              <code className="font-mono text-[10px]">curl: (22) The requested URL returned error: 404</code>, this
              command expired or was already used. Generate a new one.
            </p>
          </div>
          {advanced && (
            <CodeBlock
              text={advanced}
              label="Full install script (what the short command runs)"
              copyLabel="Copy full script"
              ariaLabel="Copy full install script"
              target="advanced"
              copied={copied}
              onCopy={onCopy}
              maxHeight="max-h-56"
            />
          )}
          {resp.ca_sha256 && <CAFingerprint sha={resp.ca_sha256} copied={copied} onCopy={onCopy} />}
        </Disclosure>
      )}
    </>
  );
}
