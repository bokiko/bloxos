import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { commandFeedback, bulkCommandFeedback, selectionAfterBulkAttempt } from "./command-feedback.mjs";

test("accepted is not completed, and HTTP errors cannot claim acceptance", () => {
  assert.equal(commandFeedback(true, { accepted: true }, "Restarted").type, "info");
  assert.match(commandFeedback(true, { accepted: true }, "Restarted").message, /not confirmed/);
  assert.equal(commandFeedback(false, { accepted: true }, "Restarted").type, "error");
  assert.deepEqual(commandFeedback(true, { success: true }, "Restarted"), { type: "success", message: "Restarted" });
  assert.equal(commandFeedback(true, { success: false, error: "denied" }, "Restarted").message, "Failed: denied");
  assert.equal(commandFeedback(true, null, "Restarted").type, "error");
});

test("bulk results distinguish sent, completed, offline and missing replies", () => {
  assert.equal(bulkCommandFeedback(true, { results: [{ accepted: true }] }, 1, "restart_service").type, "info");
  assert.equal(bulkCommandFeedback(true, { results: [{ success: true }] }, 1, "restart_service").type, "success");
  const partial = bulkCommandFeedback(true, { results: [{ accepted: true }, { error: "offline" }] }, 3, "restart_service");
  assert.equal(partial.type, "error");
  assert.match(partial.message, /0 completed, 1 sent \(unconfirmed\), 2 failed/);
  assert.equal(bulkCommandFeedback(false, {}, 2, "restart_service").type, "error");
});

test("single reboot acknowledgement is informational, not completed recovery", () => {
  const feedback = commandFeedback(true, { success: true }, "Reboot command acknowledged by host", "reboot");
  assert.equal(feedback.type, "info");
  assert.match(feedback.message, /recovery is unconfirmed/);
  assert.equal(commandFeedback(true, { success: false, error: "denied" }, "Reboot", "reboot").type, "error");
});

test("bulk reboot acknowledgements never claim completed recovery", () => {
  const acknowledged = bulkCommandFeedback(true, { results: [{ success: true }] }, 1, "reboot");
  assert.equal(acknowledged.type, "info");
  assert.match(acknowledged.message, /1 acknowledged \(reboot\/recovery unconfirmed\)/);
  assert.doesNotMatch(acknowledged.message, /completed/);
  const mixed = bulkCommandFeedback(true, { results: [{ success: true }, { accepted: true }, { error: "denied" }] }, 3, "reboot");
  assert.equal(mixed.type, "error");
  assert.match(mixed.message, /1 acknowledged .*1 sent .*1 failed/);
  assert.doesNotMatch(mixed.message, /completed/);
  assert.equal(bulkCommandFeedback(true, { results: [{ success: true }] }, 1, "restart_service").type, "success");
});

test("non-2xx bulk replies cannot claim acceptance or completion from their body", () => {
  for (const command of ["reboot", "restart_service"]) {
    const feedback = bulkCommandFeedback(false, { results: [{ accepted: true }, { success: true }] }, 2, command);
    assert.equal(feedback.type, "error");
    assert.match(feedback.message, /completion is unknown/);
    assert.doesNotMatch(feedback.message, /sent|acknowledged|completed/);
  }
});

test("finished bulk attempts clear exactly the attempted machines, never silently retryable", () => {
  // Mixed accepted/failed: every attempted id is removed so an explicit
  // retry cannot resend to already-processed machines.
  const mixed = selectionAfterBulkAttempt(["a", "b", "c"], ["a", "b"]);
  assert.deepEqual([...mixed], ["c"]);
  // Interrupted/unknown completion: same clearing (the attempt may have
  // reached some machines; the user must re-select explicitly).
  const interrupted = selectionAfterBulkAttempt(["a", "b"], ["a", "b"]);
  assert.equal(interrupted.size, 0);
  // Selections made while the request was in flight are preserved.
  const inflight = selectionAfterBulkAttempt(["a", "b", "new-during-flight"], ["a", "b"]);
  assert.deepEqual([...inflight], ["new-during-flight"]);
  // Attempted ids that are no longer selected cannot delete unrelated ones,
  // and neither input is mutated.
  const before = new Set(["x", "y"]);
  const attempted = ["a", "b", "x"];
  const unselected = selectionAfterBulkAttempt(before, attempted);
  assert.deepEqual([...unselected], ["y"]);
  assert.deepEqual([...before], ["x", "y"]);
});

test("both bulk handlers clear the attempted snapshot in finally", () => {
  // Source-shape guard for the P2 fix: each bulk handler must subtract the
  // attempted snapshot inside its finally block (every outcome), and reset
  // the loading flag there. Catches a regression that reintroduces
  // conditional clearing (which made mixed/interrupted attempts retryable).
  const src = readFileSync(new URL("../app/page.tsx", import.meta.url), "utf8");
  for (const name of ["handleBulkReboot", "handleBulkRestart"]) {
    const m = src.match(new RegExp("const " + name + " = useCallback\\(async[\\s\\S]*?\\}, \\["));
    assert.ok(m, `${name} not found`);
    assert.match(m[0], /finally \{[\s\S]*?selectionAfterBulkAttempt\(prev, attempted\)/, `${name} must clear the attempted snapshot in finally`);
    assert.match(m[0], /finally \{[\s\S]*?setBulkLoading\(false\)/, `${name} must reset loading in finally`);
    assert.doesNotMatch(m[0], /if \(feedback\.type !== "error"\) setSelected/, `${name} must not clear selection conditionally`);
  }
});
