"use client";

import { useState, useCallback } from "react";
import { Copy, Check, Terminal } from "lucide-react";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { HUB_URL, getStoredToken } from "@/lib/session";

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
      const authToken = getStoredToken();
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

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) handleClose(); }}>
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-lg" showCloseButton>
        <DialogHeader>
          <div className="flex items-center gap-2.5">
            <div className="p-1.5 rounded-lg bg-blox-blue/10">
              <Terminal className="w-4 h-4 text-blox-blue" />
            </div>
            <DialogTitle className="text-blox-text">Add Machine</DialogTitle>
          </div>
        </DialogHeader>

        <div className="space-y-4">
          {!token ? (
            <>
              <DialogDescription className="text-blox-muted text-xs leading-relaxed">
                Generate a one-time install token and run the command on the target machine.
                The token expires in 15 minutes.
              </DialogDescription>
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
              <div>
                <label className="text-[10px] text-blox-muted uppercase tracking-wider mb-1.5 block font-medium">
                  Install Command
                </label>
                <div className="relative">
                  <pre className="text-[10px] text-blox-text bg-blox-bg border border-blox-border rounded-xl p-3 pr-10 overflow-x-auto font-mono leading-relaxed whitespace-pre-wrap break-all">
                    {command}
                  </pre>
                  <button
                    onClick={copyCommand}
                    className="absolute top-2 right-2 p-1.5 rounded-lg hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors"
                    title="Copy to clipboard"
                  >
                    {copied ? (
                      <Check className="w-3.5 h-3.5 text-emerald-400" />
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
