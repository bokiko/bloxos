"use client";

import { useState } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useToast } from "./Toast";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { getStoredToken } from "@/lib/session";

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
      const token = getStoredToken();
      const headers: Record<string, string> = { "Content-Type": "application/json" };
      if (token) headers["Authorization"] = `Bearer ${token}`;
      const res = await fetch(`${hubUrl}/api/machines/${machineId}/command`, {
        method: "POST",
        headers,
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
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-sm" showCloseButton={false}>
        <DialogHeader>
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-xl bg-red-500/10">
              <AlertTriangle className="w-5 h-5 text-red-400" />
            </div>
            <DialogTitle className="text-blox-text">Confirm Reboot</DialogTitle>
          </div>
          <DialogDescription className="text-blox-muted text-xs mt-2">
            Are you sure you want to reboot <span className="text-blox-text font-semibold">{hostname}</span>?
            The machine will be temporarily unavailable.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="bg-transparent border-t-blox-border">
          <Button
            variant="outline"
            size="sm"
            onClick={onClose}
            disabled={loading}
            className="text-blox-muted border-blox-border text-xs"
          >
            Cancel
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={handleReboot}
            disabled={loading}
            className="text-xs"
          >
            {loading ? <Loader2 className="w-3 h-3 animate-spin mr-1" /> : null}
            Reboot
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
