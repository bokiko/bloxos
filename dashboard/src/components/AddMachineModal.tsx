"use client";

import { useState, useCallback } from "react";
import { X, Copy, Check, Terminal } from "lucide-react";

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

interface AddMachineModalProps {
  open: boolean;
  onClose: () => void;
}

export function AddMachineModal({ open, onClose }: AddMachineModalProps) {
  const [token, setToken] = useState<string | null>(null);
  const [command, setCommand] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const generateToken = useCallback(async () => {
    setLoading(true);
    try {
      const authToken = localStorage.getItem("bloxos_token");
      const headers: Record<string, string> = {};
      if (authToken) headers["Authorization"] = `Bearer ${authToken}`;
      const res = await fetch(`${HUB_URL}/api/tokens`, { method: "POST", headers });
      const data = await res.json();
      setToken(data.token);
      setCommand(data.command);
    } catch {
      setToken(null);
      setCommand("Error generating token. Is the hub running?");
    }
    setLoading(false);
  }, []);

  const copyCommand = useCallback(() => {
    if (command) {
      navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  }, [command]);

  const handleClose = useCallback(() => {
    setToken(null);
    setCommand(null);
    setCopied(false);
    onClose();
  }, [onClose]);

  if (!open) return null;

  return (
    <>
      <div className="fixed inset-0 z-[60] bg-black/40" onClick={handleClose} />
      <div className="fixed inset-0 z-[70] flex items-center justify-center p-4">
        <div className="bg-blox-card border border-blox-border rounded-xl shadow-2xl w-full max-w-lg">
          {/* Header */}
          <div className="flex items-center justify-between px-5 py-4 border-b border-blox-border">
            <div className="flex items-center gap-2">
              <Terminal className="w-4 h-4 text-blox-blue" />
              <h2 className="text-sm font-semibold text-blox-text">Add Machine</h2>
            </div>
            <button
              onClick={handleClose}
              className="p-1.5 rounded hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          {/* Body */}
          <div className="px-5 py-5 space-y-4">
            {!token ? (
              <>
                <p className="text-xs text-blox-muted leading-relaxed">
                  Generate a one-time install token and run the command on the target machine.
                  The token expires in 15 minutes.
                </p>
                <button
                  onClick={generateToken}
                  disabled={loading}
                  className="w-full py-2.5 rounded-md bg-blox-blue/10 text-blox-blue text-xs font-medium border border-blox-blue/20 hover:bg-blox-blue/20 transition-colors disabled:opacity-50"
                >
                  {loading ? "Generating..." : "Generate Install Token"}
                </button>
              </>
            ) : (
              <>
                <div>
                  <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block">
                    Install Command
                  </label>
                  <div className="relative">
                    <pre className="text-[10px] text-blox-text bg-blox-bg border border-blox-border rounded-md p-3 pr-10 overflow-x-auto font-mono leading-relaxed whitespace-pre-wrap break-all">
                      {command}
                    </pre>
                    <button
                      onClick={copyCommand}
                      className="absolute top-2 right-2 p-1.5 rounded hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors"
                      title="Copy to clipboard"
                    >
                      {copied ? (
                        <Check className="w-3.5 h-3.5 text-blox-green" />
                      ) : (
                        <Copy className="w-3.5 h-3.5" />
                      )}
                    </button>
                  </div>
                </div>

                <div className="text-[10px] text-blox-muted space-y-1">
                  <p>Run this command as root on the target machine.</p>
                  <p>It will download the agent, create a systemd service, and start reporting metrics.</p>
                </div>

                <button
                  onClick={handleClose}
                  className="w-full py-2 rounded-md bg-blox-border text-blox-text text-xs hover:bg-blox-border/80 transition-colors"
                >
                  Done
                </button>
              </>
            )}
          </div>
        </div>
      </div>
    </>
  );
}
