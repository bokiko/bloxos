import test from "node:test";
import assert from "node:assert/strict";
import { commandFeedback, bulkCommandFeedback } from "./command-feedback.mjs";

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
