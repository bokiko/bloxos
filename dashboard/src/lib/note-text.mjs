// The pure half of machine notes: the cap, and the one line that stands in for
// a note in a table cell or on a card.
//
// Separate from lib/machine-notes.ts because that module talks to the hub —
// this one is decidable from its arguments alone, which is what makes it
// testable under `node --test`.

/** Server-side cap, mirrored from hub/main.go's notesMaxLen. */
export const NOTES_MAX_LEN = 10000;

/**
 * The first line of a note that has anything on it, with runs of whitespace
 * collapsed to single spaces.
 *
 * Returns "" for a note that is empty, whitespace-only, or not a string at
 * all. That matters: callers use a non-empty result to mean "this machine has
 * a note worth showing", so a note of three blank lines must not light up an
 * indicator that leads to nothing.
 *
 * The result is plain text and is rendered as plain text. Notes are operator
 * free text arriving from the hub; nothing here or downstream treats them as
 * HTML or markdown.
 *
 * @param {unknown} notes
 * @returns {string}
 */
export function noteSummary(notes) {
  if (typeof notes !== "string") return "";
  for (const line of notes.split("\n")) {
    const trimmed = line.trim().replace(/\s+/g, " ");
    if (trimmed) return trimmed;
  }
  return "";
}

/**
 * True when this text is longer than the hub will accept. The check lives
 * beside the cap so a caller cannot enforce one without the other.
 *
 * @param {string} notes
 * @returns {boolean}
 */
export function notesTooLong(notes) {
  return typeof notes === "string" && notes.length > NOTES_MAX_LEN;
}
