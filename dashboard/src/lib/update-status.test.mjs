import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  ACTIVE_STATES,
  canRequestUpdate,
  isActiveState,
  pollPhase,
  statusLabel,
} from "./update-status.mjs";

test("active states cover the whole non-terminal transaction", () => {
  for (const s of ACTIVE_STATES) assert.ok(isActiveState(s));
  for (const s of ["succeeded", "rolled_back", "failed", "idle", ""]) assert.ok(!isActiveState(s));
});

test("button enables only when configured and idle", () => {
  assert.equal(canRequestUpdate({ available: true, status: null }), true);
  assert.equal(canRequestUpdate({ available: true, status: { state: "succeeded" } }), true);
  assert.equal(canRequestUpdate({ available: true, status: { state: "failed" } }), true);
  assert.equal(canRequestUpdate({ available: true, status: { state: "installing" } }), false);
  assert.equal(canRequestUpdate({ available: true, status: { state: "rolling_back" } }), false);
  assert.equal(canRequestUpdate({ available: false }), false);
  assert.equal(canRequestUpdate(null), false);
  assert.equal(canRequestUpdate({}), false); // malformed response never enables
});

test("status labels are human and bounded", () => {
  assert.equal(statusLabel("backing_up"), "Backing up");
  assert.equal(statusLabel("succeeded"), "Update complete");
  assert.equal(statusLabel("rolled_back"), "Rolled back to previous build");
  assert.equal(statusLabel("nonsense"), "nonsense");
  assert.equal(statusLabel(""), "Unknown");
});

test("polling tolerates the expected hub downtime without faking outcome", () => {
  const active = { phase: "active", state: "installing", message: "switching" };
  // Hub restarts mid-update: reconnecting, last known state preserved.
  const recon = pollPhase(active, null, true);
  assert.equal(recon.phase, "reconnecting");
  assert.equal(recon.state, "installing");
  // Recovery: next successful poll resumes real state.
  const recovered = pollPhase(recon, { available: true, status: { state: "verifying", message: "checking" } }, false);
  assert.equal(recovered.phase, "active");
  assert.equal(recovered.state, "verifying");
  // Terminal outcomes land exactly.
  assert.equal(pollPhase(recon, { available: true, status: { state: "succeeded" } }, false).phase, "succeeded");
  assert.equal(pollPhase(recon, { available: true, status: { state: "failed", message: "boom" } }, false).phase, "failed");
  assert.equal(pollPhase(recon, { available: true, status: { state: "rolled_back" } }, false).phase, "rolled_back");
  // Unconfigured is never shown as an active update.
  assert.equal(pollPhase(active, { available: false, reason: "updater not configured" }, false).phase, "unavailable");
  // Fresh unreachable with no prior active state is just unreachable.
  assert.equal(pollPhase(null, null, true).phase, "unreachable");
});

test("a terminal status from a different request is never credited to ours", () => {
  const active = { phase: "active", state: "checking", message: "" };
  // Old run's "succeeded" must not make the new request look done.
  const stale = pollPhase(active, { available: true, status: { state: "succeeded", request_id: "req-old" } }, false, "req-new");
  assert.equal(stale.phase, "accepted");
  // Still waiting while the worker picks it up; non-terminal states flow through.
  const verifying = pollPhase(stale, { available: true, status: { state: "verifying", request_id: "req-new" } }, false, "req-new");
  assert.equal(verifying.phase, "active");
  // Only OUR terminal state counts.
  const done = pollPhase(verifying, { available: true, status: { state: "succeeded", request_id: "req-new" } }, false, "req-new");
  assert.equal(done.phase, "succeeded");
  // A terminal status with no request_id is also not credited to a tracked request.
  const noID = pollPhase(active, { available: true, status: { state: "succeeded" } }, false, "req-new");
  assert.equal(noID.phase, "accepted");
  // Without tracking (first load), terminal states still display normally.
  assert.equal(pollPhase(null, { available: true, status: { state: "succeeded", request_id: "x" } }, false).phase, "succeeded");
});

test("Updates tab wiring: real button, setup guidance, downtime handling", () => {
  const component = readFileSync(new URL("../components/settings/UpdatesSettings.tsx", import.meta.url), "utf8");
  assert.match(component, /Update BloxOS/, "button must be a working Update BloxOS button");
  assert.match(component, /target_version:\s*"latest"/, "button must POST only target_version latest");
  assert.match(component, /sudo bloxos-update init/, "unavailable state must include the one-time setup command");
  assert.match(component, /Reconnecting — hub is restarting/, "must surface expected downtime honestly");
  assert.match(component, /requestIDRef/, "must track the accepted request_id so stale terminal states are not credited");
  assert.match(component, /docs\/system-updates\.md/, "unavailable state must link the one-time setup guide");
  assert.match(component, /canRequestUpdate\(info\)/, "button must be gated by the shared availability rule");
  const page = readFileSync(new URL("../app/settings/page.tsx", import.meta.url), "utf8");
  assert.match(page, /TabsTrigger value="updates"/, "settings must expose the Updates tab");
  assert.match(page, /"updates"\] as const|"ai-sessions", "updates"/, "tab list must include updates");
});
