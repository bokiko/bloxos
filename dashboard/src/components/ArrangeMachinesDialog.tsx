"use client";

import { useState } from "react";
import { ArrowUp, ArrowDown, GripVertical } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from "@/components/ui/dialog";
import { moveMachine } from "@/lib/machine-order.mjs";
import type { MachineMetrics } from "@/lib/demo-data";

export function ArrangeMachinesDialog({ machines, onSave, onClose }: {
  machines: MachineMetrics[];
  onSave: (ids: string[]) => Promise<void>;
  onClose: () => void;
}) {
  // Freeze the draft while the user works: live telemetry must not move it.
  // Include all machines, even when the fleet behind the dialog is filtered.
  const [draft, setDraft] = useState(() => machines.map(m => m.machine_id));
  const [dragging, setDragging] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const names = new Map(machines.map(m => [m.machine_id, m.hostname || m.machine_id]));
  const move = (from: string, to: string) => {
    if (saving) return;
    const next = moveMachine(draft, from, to);
    setDraft(next);
    setAnnouncement(`${names.get(from) || from} moved to position ${next.indexOf(from) + 1}.`);
  };
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      // Remove deleted machines, append arrivals without disturbing the draft.
      const ids = [...draft.filter(id => names.has(id)), ...machines.map(m => m.machine_id).filter(id => !draft.includes(id))];
      await onSave(ids);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save. Your arrangement is still here; retry when connected.");
    } finally {
      setSaving(false);
    }
  };
  return (
    <Dialog open onOpenChange={open => { if (!open && !saving) onClose(); }}>
      <DialogContent className="sm:max-w-lg bg-blox-card border-blox-border text-blox-text" showCloseButton={!saving}>
        <DialogHeader>
          <DialogTitle>Arrange machines</DialogTitle>
          <DialogDescription>
            Drag machines or use the arrows. This order is saved for your account in both grid and list views.
            Status changes will not move them. All machines are included, regardless of filters.
          </DialogDescription>
        </DialogHeader>
        <ol className="max-h-[50vh] overflow-y-auto space-y-2" aria-label="Machine order">
          {draft.map((id, index) => (
            <li key={id} className={`flex items-center gap-2 rounded-lg border border-blox-border p-2 ${dragging === id ? "opacity-50" : ""}`}
              onDragOver={e => { if (dragging && !saving) { e.preventDefault(); e.dataTransfer.dropEffect = "move"; } }}
              onDrop={e => { e.preventDefault(); if (dragging) move(dragging, id); setDragging(null); }}>
              <span draggable={!saving} onDragStart={e => { setDragging(id); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", id); }}
                onDragEnd={() => setDragging(null)} title="Drag to reorder" aria-hidden="true" className="cursor-grab p-2 text-blox-muted">
                <GripVertical className="h-4 w-4" />
              </span>
              <span className="min-w-0 flex-1 truncate text-sm">{names.get(id) || "Removed machine"}</span>
              <Button variant="outline" size="sm" aria-label={`Move ${names.get(id) || id} up`} disabled={saving || index === 0}
                onClick={() => move(id, draft[index - 1])}><ArrowUp className="h-4 w-4" /></Button>
              <Button variant="outline" size="sm" aria-label={`Move ${names.get(id) || id} down`} disabled={saving || index === draft.length - 1}
                onClick={() => move(id, draft[index + 1])}><ArrowDown className="h-4 w-4" /></Button>
            </li>
          ))}
        </ol>
        <p className="sr-only" aria-live="polite">{announcement}</p>
        <p className="text-xs text-blox-muted">Manual order takes precedence over pins. New machines appear after the saved machines.</p>
        {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
        <DialogFooter>
          <Button variant="outline" disabled={saving} onClick={onClose}>Cancel</Button>
          <Button disabled={saving} onClick={() => void save()}>{saving ? "Saving…" : "Save order"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
