import test from "node:test";
import assert from "node:assert/strict";

import {
  normalizeRolloutEntry,
  rolloutEntries,
  rolloutBadge,
  rolloutCountsLabel,
  rolloutReasonLines,
  rolloutCandidateLabel,
  rolloutRecoveryAction,
  operatorPauseLabel,
} from "./rollout-status.mjs";

// The ACTUAL shape the hub sends for a missing controller: a bare string under
// the "unavailable" key, alongside object-valued platform entries.
const UNAVAILABLE_PAYLOAD = {
  unavailable: "agent rollout controller unavailable: no such table: agent_rollout_slot",
};

test("a missing controller is never shown as a healthy rollout", () => {
  const [entry] = rolloutEntries(UNAVAILABLE_PAYLOAD);
  const badge = rolloutBadge(entry);
  assert.equal(badge.tone, "critical");
  assert.equal(badge.label, "Unavailable");
  assert.match(badge.detail, /agent_rollout_slot/);
  // And no counts are invented for it. Rendering "0 updated · 0 validated"
  // beside a green badge was the live bug: the healthiest possible display for
  // a rollout that cannot run at all.
  assert.equal(rolloutCountsLabel(entry), "");
});

test("a per-platform read failure is unavailable, not absent", () => {
  const [entry] = rolloutEntries({
    "linux/amd64": { status: "unavailable", reason: "database is locked" },
  });
  assert.equal(entry.state, "unavailable");
  assert.equal(rolloutBadge(entry).tone, "critical");
  assert.match(rolloutBadge(entry).detail, /locked/);
});

test("a halted platform is critical and names its reason", () => {
  const [entry] = rolloutEntries({
    "linux/arm64": {
      platform: "linux/arm64", status: "halted",
      halt_reason: "pi-01: update was not confirmed within the attempt window",
      summary: "halted — needs an operator", updated: 1, validated: 1,
    },
  });
  const badge = rolloutBadge(entry);
  assert.equal(badge.tone, "critical");
  assert.equal(badge.label, "Halted");
  assert.match(badge.detail, /pi-01/);
  // A halt does not erase what already shipped.
  assert.match(rolloutCountsLabel(entry), /1 observed on this build/);
});

test("an active platform mid-dwell reports validating, not silence", () => {
  const [entry] = rolloutEntries({
    "linux/amd64": {
      status: "active", summary: "1 validating",
      updated: 1, validated: 0, validating: 1, pending: 0,
    },
  });
  assert.equal(rolloutBadge(entry).label, "Automatic");
  const label = rolloutCountsLabel(entry);
  assert.match(label, /1 validating/);
  assert.match(label, /1 observed on this build/);
});

test("withheld and failed machines are nameable", () => {
  const [entry] = rolloutEntries({
    "windows/amd64": {
      status: "active", updated: 0, validated: 0, withheld: 1, failed: 1,
      withheld_reasons: { drops: "no pinned update key" },
      failed_reasons: { "win-02": "update was not confirmed within the attempt window" },
    },
  });
  const lines = rolloutReasonLines(entry);
  assert.ok(lines.some((line) => line.includes("drops") && line.includes("pinned")));
  assert.ok(lines.some((line) => line.includes("win-02")));
});

test("counts state their scope rather than implying a fleet total", () => {
  const [entry] = rolloutEntries({
    "linux/amd64": { status: "active", updated: 2, validated: 2 },
  });
  // "2 observed on this build", not "2 updated" and not "2 on this build":
  // the underlying flag is a latch, so it is a statement about what this
  // rollout has seen, not about what those machines are running now. Machines
  // that were already current never needed a slot and are absent entirely.
  assert.match(rolloutCountsLabel(entry), /2 observed on this build/);
  assert.doesNotMatch(rolloutCountsLabel(entry), /fleet/);
});

test("a malformed entry is unavailable rather than silently empty", () => {
  assert.equal(normalizeRolloutEntry("linux/amd64", null).state, "unavailable");
  assert.equal(normalizeRolloutEntry("linux/amd64", 42).state, "unavailable");
});

// The fleet-level flag means one thing only: whether a person pressed pause.
// Labelling it "Rollout: Active" survived the automatic breaker's removal and
// became wrong — a halted platform, or a hub with no controller, still showed
// Active.
test("the fleet control is labelled as the operator pause it is", () => {
  assert.equal(operatorPauseLabel(false), "Off");
  assert.equal(operatorPauseLabel(true), "On");
});

test("an absent agent_rollout yields nothing to render", () => {
  assert.deepEqual(rolloutEntries(undefined), []);
  assert.deepEqual(rolloutEntries(null), []);
});

// An empty object is what a truncated response, an older hub, or a field
// rename produces. Treating it as active gave the healthiest possible badge —
// the same defect as the string sentinel, reached by a different route.
test("an entry with no status is unavailable, not a green zero", () => {
  const [entry] = rolloutEntries({ "linux/amd64": {} });
  assert.equal(entry.state, "unavailable");
  const badge = rolloutBadge(entry);
  assert.equal(badge.tone, "critical");
  assert.equal(badge.label, "Unavailable");
  assert.equal(rolloutCountsLabel(entry), "");
  // Control: the same entry WITH a known status is not swept up by this.
  const [ok] = rolloutEntries({ "linux/amd64": { status: "active" } });
  assert.equal(ok.state, "active");
});

test("a status this page does not understand is unavailable and says so", () => {
  const [entry] = rolloutEntries({ "linux/amd64": { status: "draining", updated: 9 } });
  assert.equal(entry.state, "unavailable");
  assert.match(rolloutBadge(entry).detail, /draining/);
  // Its counts are not borrowed to decorate a state we cannot interpret.
  assert.equal(rolloutCountsLabel(entry), "");
});

// A slot is RESERVED before the socket write, so "pending" includes attempts
// that were never sent — and a resume opens one for machines already running
// the candidate, which are never announced to at all. Both "offered" and
// "pending offers" assert something about a send that the hub cannot know.
test("pending is labelled as pending attempts, never as an offer", () => {
  const [entry] = rolloutEntries({
    "linux/amd64": { status: "active", updated: 0, validated: 0, pending: 2 },
  });
  const label = rolloutCountsLabel(entry);
  assert.match(label, /2 pending attempts/);
  assert.doesNotMatch(label, /offer/);
});

// A halt sets no fleet flag. With the pause off, the old page offered only a
// Pause button — the operator could not retry at all without first pausing
// something that was not running.
test("a halted platform offers a retry even with the operator pause off", () => {
  const entries = rolloutEntries({
    "linux/amd64": { status: "halted", halt_reason: "pi-01: update was not confirmed within the attempt window" },
    "windows/amd64": { status: "active", updated: 1, validated: 1 },
  });
  const action = rolloutRecoveryAction(entries, false);
  assert.equal(action.retryAvailable, true);
  assert.deepEqual(action.halted, ["linux/amd64"]);
  assert.match(action.label, /Retry halted/);
  // One endpoint, fleet-wide: the wording must not promise per-platform scope.
  assert.match(action.detail, /every halted platform/);
});

test("with the pause on, one action clears it and retries", () => {
  const entries = rolloutEntries({ "linux/amd64": { status: "halted", halt_reason: "x" } });
  const action = rolloutRecoveryAction(entries, true);
  assert.equal(action.retryAvailable, true);
  assert.match(action.label, /Resume and retry/);
});

test("nothing halted means nothing to retry", () => {
  const entries = rolloutEntries({ "linux/amd64": { status: "active", updated: 2, validated: 2 } });
  assert.equal(rolloutRecoveryAction(entries, false).retryAvailable, false);
  assert.equal(rolloutRecoveryAction(entries, false).detail, "");
});

// An unreadable controller is not a halt. Resume refuses outright without a
// controller, so offering retry would promise a recovery that cannot happen.
test("an unavailable rollout never claims to be resumable", () => {
  const entries = rolloutEntries({
    unavailable: "agent rollout controller unavailable: no such table: agent_rollout_slot",
  });
  const action = rolloutRecoveryAction(entries, false);
  assert.equal(action.retryAvailable, false);
  assert.deepEqual(action.halted, []);
  // Control: the same call DOES offer retry when something is genuinely halted.
  assert.equal(rolloutRecoveryAction(rolloutEntries({ p: { status: "halted" } }), false).retryAvailable, true);
});

// The counts describe the build the rollout is tracking, which is not always
// the build the hub now serves: with the pause on, no reservation runs, so a
// new binary can be served while the rollout still describes the previous one.
test("each platform names the build its counts are about", () => {
  const sha = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90";
  const [entry] = rolloutEntries({
    "linux/amd64": { status: "active", candidate_sha: sha, updated: 1, validated: 1 },
  });
  const label = rolloutCandidateLabel(entry);
  assert.equal(label.short, "a1b2c3d");
  assert.equal(label.full, sha);
});

test("an unavailable entry names no build, because it has no counts either", () => {
  const [entry] = rolloutEntries({ unavailable: "controller unavailable" });
  assert.equal(rolloutCandidateLabel(entry), null);
  // And a platform whose candidate is missing or junk does not get a fake one.
  const [empty] = rolloutEntries({ "linux/amd64": { status: "active" } });
  assert.equal(rolloutCandidateLabel(empty), null);
  const [junk] = rolloutEntries({ "linux/amd64": { status: "active", candidate_sha: "n/a" } });
  assert.equal(rolloutCandidateLabel(junk), null);
});
