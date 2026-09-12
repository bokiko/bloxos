import test from "node:test";
import assert from "node:assert/strict";

import {
  normalizeRolloutEntry,
  rolloutEntries,
  rolloutBadge,
  rolloutCountsLabel,
  rolloutReasonLines,
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
      halt_reason: "pi-01: never accepted the update offer",
      summary: "halted — needs an operator", updated: 1, validated: 1,
    },
  });
  const badge = rolloutBadge(entry);
  assert.equal(badge.tone, "critical");
  assert.equal(badge.label, "Halted");
  assert.match(badge.detail, /pi-01/);
  // A halt does not erase what already shipped.
  assert.match(rolloutCountsLabel(entry), /1 on this build/);
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
  assert.match(label, /1 on this build/);
});

test("withheld and failed machines are nameable", () => {
  const [entry] = rolloutEntries({
    "windows/amd64": {
      status: "active", updated: 0, validated: 0, withheld: 1, failed: 1,
      withheld_reasons: { drops: "no pinned update key" },
      failed_reasons: { "win-02": "never accepted the update offer" },
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
  // "2 on this build", not "2 updated" — machines that were already current
  // never needed a slot and are not represented here at all.
  assert.match(rolloutCountsLabel(entry), /2 on this build/);
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
