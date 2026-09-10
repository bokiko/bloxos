"use client";

import { useState, useCallback, useMemo } from "react";
import { Edit3, Save, X } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useToast } from "@/components/Toast";
import { NOTES_MAX_LEN, saveMachineNotes } from "@/lib/machine-notes";
import {
  MF_BUTTON,
  MF_BUTTON_QUIET,
  MF_PANEL_HEAD,
  MF_PANEL_TITLE,
} from "@/lib/monoform-classes";

interface MachineNotesProps {
  machineId: string;
  initialNotes: string;
}

// PHASE12-NOTE: parent (`MachineDetailPage`) is expected to mount this
// component with `key={initialNotes}` (or remount when the underlying
// machine changes) so we don't need a useEffect that resets state from
// props — that pattern is the React 19 set-state-in-effect anti-pattern
// the spec explicitly calls out. State here only reacts to user input
// or to a successful PUT, both of which originate in this component.
export function MachineNotes({ machineId, initialNotes }: MachineNotesProps) {
  const { hasScope } = useAuth();
  const { addToast } = useToast();
  const canEdit = hasScope("fleet.metadata");

  const [notes, setNotes] = useState(initialNotes);
  const [draft, setDraft] = useState(initialNotes);
  const [isEditing, setIsEditing] = useState(false);
  const [saving, setSaving] = useState(false);

  const startEdit = useCallback(() => {
    setDraft(notes);
    setIsEditing(true);
  }, [notes]);

  const cancelEdit = useCallback(() => {
    setDraft(notes);
    setIsEditing(false);
  }, [notes]);

  // The PUT itself lives in lib/machine-notes.ts — the Overview writes the
  // same notes through the same call, so there is one request shape, one
  // length cap and one error message for both.
  const save = useCallback(async () => {
    if (saving) return;
    setSaving(true);
    try {
      await saveMachineNotes(machineId, draft);
      setNotes(draft);
      setIsEditing(false);
      addToast("success", "Notes saved");
    } catch (e) {
      addToast("error", e instanceof Error ? e.message : "Failed to save notes");
    } finally {
      setSaving(false);
    }
  }, [draft, machineId, addToast, saving]);

  // Linkify URLs for the read view. Plain text — no markdown.
  const renderedNotes = useMemo(() => renderNotes(notes), [notes]);

  return (
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <h2 className={MF_PANEL_TITLE}>Notes</h2>
        {!isEditing && canEdit && (
          <button type="button" onClick={startEdit} className={MF_BUTTON}>
            <Edit3 className="w-3.5 h-3.5" aria-hidden />
            {notes ? "Edit" : "Add notes"}
          </button>
        )}
      </div>

      <div className="px-6 py-5">
        {isEditing ? (
          <div className="space-y-3">
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value.slice(0, NOTES_MAX_LEN))}
              placeholder="Free-form notes about this machine. Plain text. URLs are linkified in the read view."
              className="min-h-[220px] w-full resize-y rounded-[10px] border border-border-default bg-surface-sunken p-3.5 font-mono text-xs leading-relaxed text-text-primary placeholder:text-text-disabled focus:border-accent focus:outline-none"
              maxLength={NOTES_MAX_LEN}
              aria-label="Machine notes"
              autoFocus
            />
            <div className="flex items-center justify-between gap-2">
              <span
                className={`mf-metric text-[11px] ${
                  draft.length > NOTES_MAX_LEN * 0.95 ? "text-status-warning" : "text-text-tertiary"
                }`}
              >
                {draft.length} / {NOTES_MAX_LEN}
              </span>
              <div className="flex items-center gap-2">
                <button type="button" onClick={cancelEdit} disabled={saving} className={MF_BUTTON_QUIET}>
                  <X className="w-3.5 h-3.5" aria-hidden />
                  Cancel
                </button>
                <button
                  type="button"
                  onClick={save}
                  disabled={saving}
                  className="mf-action inline-flex items-center gap-2"
                >
                  <Save className="w-3.5 h-3.5" aria-hidden />
                  {saving ? "Saving…" : "Save"}
                </button>
              </div>
            </div>
          </div>
        ) : notes ? (
          <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-text-primary">
            {renderedNotes}
          </pre>
        ) : (
          <p className="text-[13px] text-text-tertiary">
            No notes yet.
            {canEdit ? " Use “Add notes” to record why this machine exists." : ""}
          </p>
        )}
      </div>
    </section>
  );
}

// renderNotes splits the raw note text on URL boundaries and replaces
// each URL with an external link. URLs themselves are kept verbatim
// in the surrounding text — we never modify what the user typed.
function renderNotes(text: string): React.ReactNode[] {
  const urlRegex = /\b(https?:\/\/[^\s<>"]+)/g;
  const out: React.ReactNode[] = [];
  let lastIdx = 0;
  let key = 0;
  let match: RegExpExecArray | null;

  while ((match = urlRegex.exec(text)) !== null) {
    if (match.index > lastIdx) {
      out.push(text.slice(lastIdx, match.index));
    }
    const url = match[1];
    out.push(
      <a
        key={`u${key++}`}
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        className="break-all text-accent underline decoration-dotted underline-offset-2 hover:text-accent-hover"
      >
        {url}
      </a>
    );
    lastIdx = match.index + url.length;
  }
  if (lastIdx < text.length) {
    out.push(text.slice(lastIdx));
  }
  return out;
}
