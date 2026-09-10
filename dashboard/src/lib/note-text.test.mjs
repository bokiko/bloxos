import test from "node:test";
import assert from "node:assert/strict";

import { NOTES_MAX_LEN, noteSummary, notesTooLong } from "./note-text.mjs";

test("noteSummary is the first line that actually says something", () => {
  assert.equal(noteSummary("Runs the nightly render"), "Runs the nightly render");
  assert.equal(noteSummary("first line\nsecond line"), "first line");
  // Leading blank lines are skipped rather than returned as an empty summary —
  // a note whose text starts on line 4 still has a summary.
  assert.equal(noteSummary("\n\n   \nreal content\nmore"), "real content");
  // Runs of whitespace collapse so a tab-aligned note does not blow a table
  // cell open with a single "word".
  assert.equal(noteSummary("owner:\t\tbokiko   (ops)"), "owner: bokiko (ops)");
});

test("a note with nothing in it has no summary", () => {
  // These are the values that must NOT light up a "has a note" indicator: an
  // indicator that leads to an empty editor is worse than no indicator.
  for (const empty of ["", "   ", "\n", "\n\n\t \n", null, undefined, 42, {}, []]) {
    assert.equal(noteSummary(empty), "", `${JSON.stringify(empty)} must have no summary`);
  }
});

test("the cap matches the hub and is enforced by length, not by line count", () => {
  assert.equal(NOTES_MAX_LEN, 10000, "must match notesMaxLen in hub/main.go");
  assert.equal(notesTooLong("x".repeat(NOTES_MAX_LEN)), false, "exactly at the cap is accepted");
  assert.equal(notesTooLong("x".repeat(NOTES_MAX_LEN + 1)), true);
  assert.equal(notesTooLong("line\n".repeat(500)), false);
});
