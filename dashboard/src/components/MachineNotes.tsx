"use client";

import { useState, useCallback, useMemo } from "react";
import { Edit3, Save, X, FileText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/contexts/AuthContext";
import { useToast } from "@/components/Toast";
import { HUB_URL } from "@/lib/session";

const NOTES_MAX_LEN = 10000;

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

  const save = useCallback(async () => {
    if (saving) return;
    if (draft.length > NOTES_MAX_LEN) {
      addToast("error", `Notes exceed ${NOTES_MAX_LEN} characters`);
      return;
    }
    setSaving(true);
    try {
      const token = localStorage.getItem("bloxos_token");
      const res = await fetch(`${HUB_URL}/api/machines/${machineId}/notes`, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ notes: draft }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        addToast("error", err.error || "Failed to save notes");
        return;
      }
      setNotes(draft);
      setIsEditing(false);
      addToast("success", "Notes saved");
    } catch {
      addToast("error", "Failed to save notes");
    } finally {
      setSaving(false);
    }
  }, [draft, machineId, addToast, saving]);

  // Linkify URLs for the read view. Plain text — no markdown.
  const renderedNotes = useMemo(() => renderNotes(notes), [notes]);

  return (
    <div className="bg-blox-card border border-blox-border rounded-xl p-5">
      <div className="flex items-center justify-between gap-2 mb-4">
        <div className="flex items-center gap-2.5">
          <div className="p-1.5 rounded-lg bg-blox-blue/10">
            <FileText className="w-3.5 h-3.5 text-blox-blue" />
          </div>
          <h3 className="text-sm font-semibold text-blox-text">Notes</h3>
        </div>
        {!isEditing && canEdit && (
          <Button
            onClick={startEdit}
            variant="outline"
            size="sm"
            className="h-7 px-2 text-xs gap-1.5 border-blox-border text-blox-text"
          >
            <Edit3 className="w-3 h-3" />
            {notes ? "Edit" : "Add notes"}
          </Button>
        )}
      </div>

      {isEditing ? (
        <div className="space-y-3">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value.slice(0, NOTES_MAX_LEN))}
            placeholder="Free-form notes about this machine. Plain text. URLs are linkified in the read view."
            className="w-full min-h-[180px] bg-blox-bg border border-blox-border rounded-lg p-3 text-xs text-blox-text font-mono leading-relaxed focus:outline-none focus:border-blox-blue/40 resize-y"
            maxLength={NOTES_MAX_LEN}
            autoFocus
          />
          <div className="flex items-center justify-between gap-2">
            <span
              className={`text-[10px] tabular-nums font-mono ${
                draft.length > NOTES_MAX_LEN * 0.95
                  ? "text-amber-400"
                  : "text-blox-muted"
              }`}
            >
              {draft.length} / {NOTES_MAX_LEN}
            </span>
            <div className="flex items-center gap-2">
              <Button
                onClick={cancelEdit}
                variant="outline"
                size="sm"
                disabled={saving}
                className="h-7 px-2 text-xs gap-1.5 border-blox-border text-blox-muted"
              >
                <X className="w-3 h-3" />
                Cancel
              </Button>
              <Button
                onClick={save}
                size="sm"
                disabled={saving}
                className="h-7 px-2 text-xs gap-1.5"
              >
                <Save className="w-3 h-3" />
                {saving ? "Saving..." : "Save"}
              </Button>
            </div>
          </div>
        </div>
      ) : notes ? (
        <pre className="whitespace-pre-wrap break-words text-xs text-blox-text font-mono leading-relaxed">
          {renderedNotes}
        </pre>
      ) : (
        <p className="text-xs text-blox-muted/70 italic">
          No notes yet.
          {canEdit ? " Click “Add notes” to get started." : ""}
        </p>
      )}
    </div>
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
        className="text-blox-blue underline decoration-dotted underline-offset-2 hover:text-blox-blue/80 break-all"
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
