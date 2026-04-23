"use client";

import { useState, useCallback } from "react";
import { Server, CheckCircle } from "lucide-react";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getAuthHeaders, HUB_URL } from "@/lib/session";

type AdapterType = "proxmox" | "synology";
export type TlsMode = "system" | "custom_ca" | "insecure";

export interface APIMachineTLSMeta {
  mode: TlsMode;
  has_custom_ca?: boolean;
}

export interface EditableAPIMachine {
  id: string;
  name: string;
  adapter_type: AdapterType;
  base_url: string;
  poll_interval_secs: number;
  tls_config?: APIMachineTLSMeta;
}

interface AddAPIMachineModalProps {
  open: boolean;
  onClose: () => void;
  onSaved?: () => void;
  machine?: EditableAPIMachine | null;
}

function clampInterval(v: number) {
  return Math.max(30, Math.min(3600, v || 60));
}

export function AddAPIMachineModal({ open, onClose, onSaved, machine }: AddAPIMachineModalProps) {
  const editing = !!machine;

  const [name, setName] = useState(machine?.name || "");
  const [adapterType, setAdapterType] = useState<AdapterType>(machine?.adapter_type || "synology");
  const [baseUrl, setBaseUrl] = useState(machine?.base_url || "");
  const [pollInterval, setPollInterval] = useState(machine?.poll_interval_secs || 60);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");
  const [tlsMode, setTlsMode] = useState<TlsMode>(machine?.tls_config?.mode || "system");
  const [caCertPem, setCaCertPem] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const handleClose = useCallback(() => {
    onClose();
  }, [onClose]);

  const handleSubmit = useCallback(async () => {
    if (!name.trim() || !baseUrl.trim()) {
      setError("Name and URL are required.");
      return;
    }

    const payload: Record<string, unknown> = {};
    const trimmedName = name.trim();
    const trimmedBaseUrl = baseUrl.trim();
    const clampedPollInterval = clampInterval(pollInterval);
    const initialTlsMode = machine?.tls_config?.mode || "system";

    if (!editing || trimmedName !== machine?.name) payload.name = trimmedName;
    if (!editing || adapterType !== machine?.adapter_type) payload.adapter_type = adapterType;
    if (!editing || trimmedBaseUrl !== machine?.base_url) payload.base_url = trimmedBaseUrl;
    if (!editing || clampedPollInterval !== machine?.poll_interval_secs) payload.poll_interval_secs = clampedPollInterval;

    let authConfig: Record<string, string> | null = null;
    if (adapterType === "synology") {
      const trimmedUsername = username.trim();
      const trimmedPassword = password.trim();
      if (!editing) {
        if (!trimmedUsername || !trimmedPassword) {
          setError("Username and password are required for Synology.");
          return;
        }
        authConfig = { username: trimmedUsername, password: trimmedPassword };
      } else if (trimmedUsername || trimmedPassword) {
        if (!trimmedUsername || !trimmedPassword) {
          setError("Enter both username and password, or leave both blank to keep existing credentials.");
          return;
        }
        authConfig = { username: trimmedUsername, password: trimmedPassword };
      }
    } else {
      const trimmedTokenID = tokenId.trim();
      const trimmedTokenSecret = tokenSecret.trim();
      if (!editing) {
        if (!trimmedTokenID || !trimmedTokenSecret) {
          setError("Token ID and Token Secret are required for Proxmox.");
          return;
        }
        authConfig = { token_id: trimmedTokenID, token_secret: trimmedTokenSecret };
      } else if (trimmedTokenID || trimmedTokenSecret) {
        if (!trimmedTokenID || !trimmedTokenSecret) {
          setError("Enter both token fields, or leave both blank to keep existing credentials.");
          return;
        }
        authConfig = { token_id: trimmedTokenID, token_secret: trimmedTokenSecret };
      }
    }
    if (authConfig) payload.auth_config = authConfig;

    const trimmedCaPem = caCertPem.trim();
    const shouldReplaceCustomCA = tlsMode === "custom_ca" && !!trimmedCaPem;
    const tlsModeChanged = !editing || tlsMode !== initialTlsMode;
    if (tlsMode === "custom_ca" && !editing && !trimmedCaPem) {
      setError("Paste the CA certificate PEM or switch TLS mode.");
      return;
    }
    if (tlsModeChanged || shouldReplaceCustomCA) {
      if (tlsMode === "custom_ca" && !trimmedCaPem) {
        setError("Paste a new CA certificate PEM when switching to Custom CA.");
        return;
      }
      payload.tls_config = {
        mode: tlsMode,
        ca_cert_pem: tlsMode === "custom_ca" ? trimmedCaPem : "",
      };
    }

    if (editing && Object.keys(payload).length === 0) {
      setError("No changes to save.");
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const headers: Record<string, string> = getAuthHeaders({
        "Content-Type": "application/json",
      });
      const res = await fetch(
        editing ? `${HUB_URL}/api/api-machines/${machine.id}` : `${HUB_URL}/api/api-machines`,
        {
          method: editing ? "PATCH" : "POST",
          headers,
          body: JSON.stringify(payload),
        }
      );

      if (!res.ok) {
        const data = await res.json().catch(() => null);
        throw new Error(data?.error || `Failed (${res.status})`);
      }

      setSuccess(true);
      onSaved?.();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : editing ? "Failed to update machine." : "Failed to add machine.");
    } finally {
      setLoading(false);
    }
  }, [
    name,
    adapterType,
    baseUrl,
    pollInterval,
    username,
    password,
    tokenId,
    tokenSecret,
    tlsMode,
    caCertPem,
    editing,
    machine,
    onSaved,
  ]);

  const existingCustomCA = editing && machine?.tls_config?.mode === "custom_ca" && machine.tls_config?.has_custom_ca;

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) handleClose(); }}>
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-lg" showCloseButton>
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-blue/10">
              <Server className="w-4 h-4 text-blox-blue" />
            </div>
            <DialogTitle className="text-blox-text">{editing ? "Edit API Machine" : "Add API Machine"}</DialogTitle>
          </div>
        </DialogHeader>

        <div className="space-y-4">
          {success ? (
            <>
              <div className="flex flex-col items-center gap-3 py-4">
                <div className="p-2.5 rounded-xl bg-emerald-500/10">
                  <CheckCircle className="w-6 h-6 text-emerald-400" />
                </div>
                <p className="text-sm text-blox-text text-center font-medium">
                  {editing ? "Machine updated!" : "Machine added!"}
                </p>
                <p className="text-xs text-blox-muted text-center">
                  {editing ? "The poller was reloaded with the new settings." : "It will appear on the dashboard shortly."}
                </p>
              </div>
              <Button
                onClick={handleClose}
                variant="outline"
                className="w-full text-xs border-blox-border text-blox-text"
              >
                Done
              </Button>
            </>
          ) : (
            <>
              <DialogDescription className="text-blox-muted text-xs leading-relaxed">
                {editing
                  ? "Update this API-polled machine. Leave credentials blank to keep the existing secret values."
                  : "Add a machine that BloxOS polls via API (no agent required). Supports Proxmox and Synology."}
              </DialogDescription>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Name
                </label>
                <Input
                  type="text"
                  placeholder='e.g. "Dasman", "Proxmox-HP"'
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                />
              </div>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Type
                </label>
                <div className="flex gap-2">
                  {(["synology", "proxmox"] as const).map((value) => (
                    <button
                      key={value}
                      type="button"
                      onClick={() => setAdapterType(value)}
                      className={`flex-1 text-xs py-2 px-3 rounded-lg border transition-colors font-medium ${
                        adapterType === value
                          ? "bg-blox-blue/10 text-blox-blue border-blox-blue/30"
                          : "bg-blox-bg text-blox-muted border-blox-border hover:border-blox-muted/30"
                      }`}
                    >
                      {value.charAt(0).toUpperCase() + value.slice(1)}
                    </button>
                  ))}
                </div>
              </div>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  URL
                </label>
                <Input
                  type="text"
                  placeholder={adapterType === "proxmox" ? "https://192.168.3.2:8006" : "http://192.168.16.234:5000"}
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value)}
                  className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                />
              </div>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Poll Interval (seconds)
                </label>
                <Input
                  type="number"
                  min={30}
                  max={3600}
                  value={pollInterval}
                  onChange={(e) => setPollInterval(Number(e.target.value))}
                  onBlur={() => setPollInterval(clampInterval(pollInterval))}
                  className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text w-28 font-mono"
                />
              </div>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Credentials
                </label>
                {adapterType === "synology" ? (
                  <div className="space-y-2">
                    <Input
                      type="text"
                      placeholder={editing ? "Username (leave blank to keep current)" : "Username"}
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                    />
                    <Input
                      type="password"
                      placeholder={editing ? "Password (leave blank to keep current)" : "Password"}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                    />
                  </div>
                ) : (
                  <div className="space-y-2">
                    <Input
                      type="text"
                      placeholder={editing ? "Token ID (leave blank to keep current)" : "Token ID (e.g. root@pam!monitor)"}
                      value={tokenId}
                      onChange={(e) => setTokenId(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                    />
                    <Input
                      type="password"
                      placeholder={editing ? "Token Secret (leave blank to keep current)" : "Token Secret"}
                      value={tokenSecret}
                      onChange={(e) => setTokenSecret(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                    />
                  </div>
                )}
              </div>

              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  TLS Trust
                </label>
                <div className="flex gap-2">
                  {([
                    { value: "system", label: "System" },
                    { value: "custom_ca", label: "Custom CA" },
                    { value: "insecure", label: "Insecure" },
                  ] as const).map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      onClick={() => setTlsMode(option.value)}
                      className={`flex-1 text-xs py-2 px-3 rounded-lg border transition-colors font-medium ${
                        tlsMode === option.value
                          ? "bg-blox-blue/10 text-blox-blue border-blox-blue/30"
                          : "bg-blox-bg text-blox-muted border-blox-border hover:border-blox-muted/30"
                      }`}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
                <p className="mt-1.5 text-[11px] text-blox-muted leading-relaxed">
                  {tlsMode === "system" && "Use the hub's normal system trust store. Best for publicly trusted certs."}
                  {tlsMode === "custom_ca" && "Paste the API endpoint's root or self-signed CA certificate PEM."}
                  {tlsMode === "insecure" && "Temporary only. Skips TLS verification for this machine and logs a warning on every poll."}
                </p>
              </div>

              {tlsMode === "custom_ca" && (
                <div>
                  <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                    CA Certificate PEM
                  </label>
                  {existingCustomCA && !caCertPem && (
                    <p className="mb-2 text-[11px] text-blox-muted">
                      A custom CA is already stored. Paste a new PEM only if you want to replace it.
                    </p>
                  )}
                  <textarea
                    rows={7}
                    placeholder="-----BEGIN CERTIFICATE-----"
                    value={caCertPem}
                    onChange={(e) => setCaCertPem(e.target.value)}
                    className="w-full rounded-lg border border-blox-border bg-blox-bg px-3 py-2 text-xs font-mono text-blox-text placeholder:text-blox-muted/50 outline-none focus:border-blox-blue/40"
                  />
                </div>
              )}

              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
                  {error}
                </p>
              )}

              <Button
                onClick={handleSubmit}
                disabled={loading}
                variant="outline"
                className="w-full text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 text-xs"
              >
                {loading ? (editing ? "Saving..." : "Adding...") : (editing ? "Save Changes" : "Add Machine")}
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
