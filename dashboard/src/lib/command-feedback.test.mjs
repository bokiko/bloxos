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
  assert.equal(bulkCommandFeedback(true, { results: [{ accepted: true }] }, 1).type, "info");
  assert.equal(bulkCommandFeedback(true, { results: [{ success: true }] }, 1).type, "success");
  const partial = bulkCommandFeedback(true, { results: [{ accepted: true }, { error: "offline" }] }, 3);
  assert.equal(partial.type, "error");
  assert.match(partial.message, /0 completed, 1 sent \(unconfirmed\), 2 failed/);
  assert.equal(bulkCommandFeedback(false, {}, 2).type, "error");
});
