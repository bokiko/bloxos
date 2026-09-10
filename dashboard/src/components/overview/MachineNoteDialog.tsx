"use client";

// Read or edit one machine's note without leaving the Overview.
//
// This is the same note the machine detail page's Notes tab edits, written
// through the same PUT (lib/machine-notes.ts). It is a dialog rather than an
// inline textarea in a table row because a note is up to 10,000 characters of
// free text: an editor that grows a table row by 200px pushes every machine
// below it off the screen mid-edit.
//
// Notes are operator free text coming back from the hub. They are rendered as
// TEXT — a <pre> whose child is a string — never as HTML and never as markdown.

import { useState } from "react";
import { AlertTriangle, RotateCcw, Save, X } from "lucide-react";
import { NOTES_MAX_LEN } from "@/lib/machine-notes";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { NoteState } from "./useMachineNotes";

export interface MachineNoteDialogProps {
  machineID: string;
  hostname: string;
  note: NoteState;
  /** fleet.metadata. A reader without it gets the note, and no editor. */
  canEdit: boolean;
  onSave: (machineID: string, text: string) => Promise<void>;
  onReload: (machineID: string) => void;
  onClose: () => void;
}

export function MachineNoteDialog({
  machineID,
  hostname,
  note,
  canEdit,
  onSave,
  onReload,
  onClose,
}: MachineNoteDialogProps) {
  // Seeded once from the loaded note. The dialog is mounted with a key tied to
  // the machine, so switching machines remounts it rather than needing an
  // effect to copy props into state.
  const [draft, setDraft] = useState(note.text);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const loading = note.status === "loading";
  const failed = note.status === "error";

  const save = async () => {
    if (saving) return;
    setSaving(true);
    setSaveError(null);
    try {
      await onSave(machineID, draft);
      onClose();
    } catch (e) {
      // Stays open with the draft intact: closing on a failed save would throw
      // the operator's text away and leave them believing it was stored.
      setSaveError(e instanceof Error ? e.message : "Failed to save notes");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={(o) => { if (!o && !saving) onClose(); }}>
      <DialogContent
        className="border-border-subtle bg-surface-raised text-text-primary ring-0 sm:max-w-xl"
        showCloseButton={false}
      >
        <DialogHeader>
          <DialogTitle className="text-text-primary">Notes</DialogTitle>
          <DialogDescription className="mt-1 text-xs text-text-tertiary">
            <span className="mf-metric text-text-secondary">{hostname}</span> — free-form
            text stored with the machine. The same note appears on the machine page.
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <p className="py-6 text-[13px] text-text-tertiary">Loading this machine&rsquo;s note…</p>
        ) : failed ? (
          <div className="flex items-start gap-3 rounded-[10px] border border-status-critical/40 px-4 py-3">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-status-critical" aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="text-[13px] text-text-primary">
                {note.error ?? "Could not load notes"}
              </p>
              <p className="mt-1 text-xs text-text-tertiary">
                Nothing has been changed. This machine may still have a note.
              </p>
            </div>
            <button
              type="button"
              onClick={() => onReload(machineID)}
              className="mf-inline-action"
            >
              <RotateCcw className="h-3 w-3" aria-hidden />
              Retry
            </button>
          </div>
        ) : canEdit ? (
          <div className="space-y-3">
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value.slice(0, NOTES_MAX_LEN))}
              placeholder="Why this machine exists, who owns it, what not to reboot. Plain text."
              className="min-h-[200px] w-full resize-y rounded-[10px] border border-border-default bg-surface-sunken p-3.5 font-mono text-xs leading-relaxed text-text-primary placeholder:text-text-disabled focus:border-accent focus:outline-none"
              maxLength={NOTES_MAX_LEN}
              aria-label={`Notes for ${hostname}`}
              autoFocus
            />
            <div className="flex items-center justify-between gap-3">
              <span
                className={`mf-metric text-[11px] ${
                  draft.length > NOTES_MAX_LEN * 0.95 ? "text-status-warning" : "text-text-tertiary"
                }`}
              >
                {draft.length} / {NOTES_MAX_LEN}
              </span>
              {saveError && (
                <span className="min-w-0 flex-1 truncate text-right text-[12px] text-status-critical">
                  {saveError}
                </span>
              )}
            </div>
          </div>
        ) : note.text ? (
          <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap break-words rounded-[10px] border border-border-subtle bg-surface-sunken p-3.5 font-mono text-xs leading-relaxed text-text-primary">
            {note.text}
          </pre>
        ) : (
          <p className="py-6 text-[13px] text-text-tertiary">
            No notes yet. You do not have permission to add them.
          </p>
        )}

        <DialogFooter className="border-t-border-subtle bg-transparent">
          <Button
            variant="outline"
            size="sm"
            onClick={onClose}
            disabled={saving}
            className="border-border-subtle text-xs text-text-tertiary"
          >
            <X className="h-3.5 w-3.5" aria-hidden />
            {canEdit && !loading && !failed ? "Cancel" : "Close"}
          </Button>
          {canEdit && !loading && !failed && (
            <button
              type="button"
              onClick={() => void save()}
              disabled={saving}
              className="mf-action"
            >
              <Save className="h-3.5 w-3.5" aria-hidden />
              {saving ? "Saving…" : "Save note"}
            </button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
