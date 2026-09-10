"use client";

import { useState } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useToast } from "./Toast";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter,
} from "@/components/ui/dialog";
import { MF_BUTTON_DANGER, MF_BUTTON_QUIET, MF_DIALOG } from "@/lib/monoform-classes";
import { getStoredToken } from "@/lib/session";
import { commandFeedback } from "@/lib/command-feedback.mjs";

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
      const feedback = commandFeedback(res.ok, data, `Reboot command acknowledged by ${hostname}`, "reboot");
      addToast(feedback.type, feedback.message);
    } catch (err) {
      addToast("error", `Network error: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setLoading(false);
      onClose();
    }
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className={`${MF_DIALOG} sm:max-w-sm`} showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2.5">
            <AlertTriangle className="w-4 h-4 text-status-critical" aria-hidden />
            Confirm reboot
          </DialogTitle>
          <DialogDescription className="mt-2 text-[13px] leading-6 text-text-tertiary">
            Reboot <span className="font-medium text-text-primary">{hostname}</span>? The machine will
            be temporarily unavailable.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <button type="button" onClick={onClose} disabled={loading} className={MF_BUTTON_QUIET}>
            Cancel
          </button>
          <button type="button" onClick={handleReboot} disabled={loading} className={MF_BUTTON_DANGER}>
            {loading ? <Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden /> : null}
            Reboot
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
