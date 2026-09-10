"use client";

// The one place the machine-notes endpoints are called.
//
// Notes are free text the operator writes about a machine —
//   GET  /api/machines/:id/notes   (scope fleet.read)
//   PUT  /api/machines/:id/notes   (scope fleet.metadata)
// — and until now the only caller was components/MachineNotes.tsx on the
// machine detail page, with its fetch, its auth header and its length cap
// inline. The Overview now reads and writes the same notes, so the transport
// moved here rather than being written a second time: one cap, one error
// shape, one URL.
//
// Notes are NOT part of the machine list or the SSE stream — neither
// getMachinesJSON() nor the metrics payload carries the column — so a caller
// that wants a note has to ask for it per machine. That is a real cost on the
// Overview and it is why useMachineNotes() batches its reads.
//
// The text is always treated as text. It is never interpreted as HTML or
// markdown anywhere: the read views render it into a <pre> or a plain string,
// and the only enrichment is linkifying bare URLs.

import { HUB_URL, getAuthHeaders } from "@/lib/session";
import { NOTES_MAX_LEN, noteSummary, notesTooLong } from "@/lib/note-text.mjs";

// The cap and the summary rule are decidable without the network, so they live
// in lib/note-text.mjs where `node --test` can reach them. Re-exported here so
// a caller needs one import for "everything about a note".
export { NOTES_MAX_LEN, noteSummary };

/** Thrown with a message fit to show the operator directly. */
export class MachineNotesError extends Error {}

function notesURL(machineId: string): string {
  return `${HUB_URL}/api/machines/${encodeURIComponent(machineId)}/notes`;
}

/**
 * This machine's note, or "" when it has none. An unreadable response is an
 * error, never an empty note: "no note" and "we could not find out" are
 * different facts and the UI has to be able to tell them apart.
 */
export async function fetchMachineNotes(
  machineId: string,
  signal?: AbortSignal,
): Promise<string> {
  const res = await fetch(notesURL(machineId), { headers: getAuthHeaders(), signal });
  if (!res.ok) {
    throw new MachineNotesError(`Could not load notes (HTTP ${res.status})`);
  }
  const body = (await res.json().catch(() => null)) as { notes?: unknown } | null;
  return typeof body?.notes === "string" ? body.notes : "";
}

/**
 * Replace this machine's note. Rejects with the hub's own message when it has
 * one, so a server-side rule (length, permission) is reported verbatim rather
 * than as a generic failure.
 */
export async function saveMachineNotes(machineId: string, notes: string): Promise<void> {
  if (notesTooLong(notes)) {
    throw new MachineNotesError(`Notes exceed ${NOTES_MAX_LEN} characters`);
  }
  const res = await fetch(notesURL(machineId), {
    method: "PUT",
    headers: getAuthHeaders({ "Content-Type": "application/json" }),
    body: JSON.stringify({ notes }),
  });
  if (!res.ok) {
    const err = (await res.json().catch(() => null)) as { error?: unknown } | null;
    throw new MachineNotesError(
      typeof err?.error === "string" && err.error ? err.error : "Failed to save notes",
    );
  }
}

