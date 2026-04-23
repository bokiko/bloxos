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
type TlsMode = "system" | "custom_ca" | "insecure";

interface AddAPIMachineModalProps {
  open: boolean;
  onClose: () => void;
}

export function AddAPIMachineModal({ open, onClose }: AddAPIMachineModalProps) {
  const [name, setName] = useState("");
  const [adapterType, setAdapterType] = useState<AdapterType>("synology");
  const [baseUrl, setBaseUrl] = useState("");
  const [pollInterval, setPollInterval] = useState(60);
  // Synology creds
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  // Proxmox creds
  const [tokenId, setTokenId] = useState("");
  const [tokenSecret, setTokenSecret] = useState("");
  const [tlsMode, setTlsMode] = useState<TlsMode>("system");
  const [caCertPem, setCaCertPem] = useState("");

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  const resetForm = useCallback(() => {
    setName("");
    setAdapterType("synology");
    setBaseUrl("");
    setPollInterval(60);
    setUsername("");
    setPassword("");
    setTokenId("");
    setTokenSecret("");
    setTlsMode("system");
    setCaCertPem("");
    setError(null);
    setSuccess(false);
  }, []);

  const handleClose = useCallback(() => {
    resetForm();
    onClose();
  }, [onClose, resetForm]);

  const handleSubmit = useCallback(async () => {
    if (!name.trim() || !baseUrl.trim()) {
      setError("Name and URL are required.");
      return;
    }

    const authConfig =
      adapterType === "synology"
        ? { username, password }
        : { token_id: tokenId, token_secret: tokenSecret };

    if (adapterType === "synology" && (!username.trim() || !password.trim())) {
      setError("Username and password are required for Synology.");
      return;
    }
    if (adapterType === "proxmox" && (!tokenId.trim() || !tokenSecret.trim())) {
      setError("Token ID and Token Secret are required for Proxmox.");
      return;
    }
    if (tlsMode === "custom_ca" && !caCertPem.trim()) {
      setError("Paste the CA certificate PEM or switch TLS mode.");
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const headers: Record<string, string> = getAuthHeaders({
        "Content-Type": "application/json",
      });

      const res = await fetch(`${HUB_URL}/api/api-machines`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          name: name.trim(),
          adapter_type: adapterType,
          base_url: baseUrl.trim(),
          auth_config: authConfig,
          tls_config: {
            mode: tlsMode,
            ca_cert_pem: tlsMode === "custom_ca" ? caCertPem.trim() : "",
          },
          poll_interval_secs: pollInterval,
        }),
      });

      if (!res.ok) {
        const data = await res.json().catch(() => null);
        throw new Error(data?.error || `Failed (${res.status})`);
      }

      setSuccess(true);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to add machine.");
    }
    setLoading(false);
  }, [name, adapterType, baseUrl, pollInterval, username, password, tokenId, tokenSecret, tlsMode, caCertPem]);

  const clampInterval = (v: number) => Math.max(30, Math.min(3600, v || 60));

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) handleClose(); }}>
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-lg" showCloseButton>
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-blue/10">
              <Server className="w-4 h-4 text-blox-blue" />
            </div>
            <DialogTitle className="text-blox-text">Add API Machine</DialogTitle>
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
                  Machine added!
                </p>
                <p className="text-xs text-blox-muted text-center">
                  It will appear on the dashboard shortly.
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
                Add a machine that BloxOS polls via API (no agent required). Supports Proxmox and Synology.
              </DialogDescription>

              {/* Name */}
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

              {/* Type toggle */}
              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Type
                </label>
                <div className="flex gap-2">
                  {(["synology", "proxmox"] as const).map((t) => (
                    <button
                      key={t}
                      type="button"
                      onClick={() => setAdapterType(t)}
                      className={`flex-1 text-xs py-2 px-3 rounded-lg border transition-colors font-medium ${
                        adapterType === t
                          ? "bg-blox-blue/10 text-blox-blue border-blox-blue/30"
                          : "bg-blox-bg text-blox-muted border-blox-border hover:border-blox-muted/30"
                      }`}
                    >
                      {t.charAt(0).toUpperCase() + t.slice(1)}
                    </button>
                  ))}
                </div>
              </div>

              {/* URL */}
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

              {/* Poll Interval */}
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

              {/* Credentials */}
              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Credentials
                </label>
                {adapterType === "synology" ? (
                  <div className="space-y-2">
                    <Input
                      type="text"
                      placeholder="Username"
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                    />
                    <Input
                      type="password"
                      placeholder="Password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                    />
                  </div>
                ) : (
                  <div className="space-y-2">
                    <Input
                      type="text"
                      placeholder="Token ID (e.g. root@pam!monitor)"
                      value={tokenId}
                      onChange={(e) => setTokenId(e.target.value)}
                      className="h-8 text-xs bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                    />
                    <Input
                      type="password"
                      placeholder="Token Secret"
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
                  <textarea
                    rows={7}
                    placeholder="-----BEGIN CERTIFICATE-----"
                    value={caCertPem}
                    onChange={(e) => setCaCertPem(e.target.value)}
                    className="w-full rounded-lg border border-blox-border bg-blox-bg px-3 py-2 text-xs font-mono text-blox-text placeholder:text-blox-muted/50 outline-none focus:border-blox-blue/40"
                  />
                </div>
              )}

              {/* Error */}
              {error && (
                <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">
                  {error}
                </p>
              )}

              {/* Submit */}
              <Button
                onClick={handleSubmit}
                disabled={loading}
                variant="outline"
                className="w-full text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 text-xs"
              >
                {loading ? "Adding..." : "Add Machine"}
              </Button>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
