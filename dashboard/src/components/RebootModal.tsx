"use client";

import { useState } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useToast } from "./Toast";

interface RebootModalProps {
  hostname: string;
  machineId: string;
  hubUrl: string;
  onClose: () => void;
}

export function RebootModal({ hostname, machineId, hubUrl, onClose }: RebootModalProps) {
  const [loading, setLoading] = useState(false);
  const { addToast } = useToast();

  async function handleReboot() {
    setLoading(true);
    try {
      const res = await fetch(`${hubUrl}/api/machines/${machineId}/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: "reboot", target: "" }),
      });
      const data = await res.json();
      if (data.success) {
        addToast("success", `Reboot command sent to ${hostname}`);
      } else {
        addToast("error", `Reboot failed: ${data.error || "unknown error"}`);
      }
    } catch (err) {
      addToast("error", `Network error: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setLoading(false);
      onClose();
    }
  }

  return (
    <div className="fixed inset-0 z-[90] flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative bg-blox-card border border-blox-border rounded-lg p-6 max-w-sm w-full mx-4 shadow-2xl">
        <div className="flex items-center gap-3 mb-4">
          <div className="p-2 rounded-lg bg-blox-red/10">
            <AlertTriangle className="w-5 h-5 text-blox-red" />
          </div>
          <h3 className="text-sm font-semibold text-blox-text">Confirm Reboot</h3>
        </div>
        <p className="text-xs text-blox-muted mb-6">
          Are you sure you want to reboot <span className="text-blox-text font-semibold">{hostname}</span>?
          The machine will be temporarily unavailable.
        </p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            disabled={loading}
            className="px-4 py-2 rounded-md text-xs text-blox-muted border border-blox-border hover:bg-blox-border/30 transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleReboot}
            disabled={loading}
            className="px-4 py-2 rounded-md text-xs text-white bg-blox-red hover:bg-blox-red/80 transition-colors flex items-center gap-2"
          >
            {loading ? <Loader2 className="w-3 h-3 animate-spin" /> : null}
            Reboot
          </button>
        </div>
      </div>
    </div>
  );
}
