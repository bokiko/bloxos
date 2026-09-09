import test from "node:test";
import assert from "node:assert/strict";
import {
  binaryReleaseLabel,
  binaryStateLabel,
  buildBinaryCards,
  agentStatusLabel,
  agentProtocolNote,
} from "./versions-honesty.mjs";

test("release labels: numbered, explicit-legacy, and unknown are distinct", () => {
  assert.equal(binaryReleaseLabel({ release: 5, sha: "abc" }, true), "Release 5");
  assert.equal(binaryReleaseLabel({ release: 0, sha: "abc" }, true), "Legacy (unnumbered)");
  // Older hubs omit the field entirely: unknown, not legacy.
  assert.equal(binaryReleaseLabel({ sha: "abc" }, false), "Release unknown (older hub)");
  assert.equal(binaryReleaseLabel({ sha: "abc" }, true), "Release unknown (older hub)");
});

test("availability never claims trust", () => {
  const ok = binaryStateLabel({ sha: "abc", error: "" });
  assert.equal(ok.available, true);
  assert.equal(ok.label, "Available");
  assert.doesNotMatch(ok.label, /trusted/i);
  const bad = binaryStateLabel({ sha: "", error: "no binary for arch" });
  assert.equal(bad.available, false);
  assert.equal(bad.label, "Unavailable");
  assert.equal(bad.detail, "no binary for arch");
});

test("per-arch cards always include Linux ARM64, even when unserved", () => {
  const data = {
    agent_binaries_by_arch: {
      linux: { amd64: { sha: "aaa", mtime: "2026-09-08T00:00:00Z", release: 5 } },
      windows: { amd64: { sha: "bbb", mtime: "2026-09-08T00:00:00Z", release: 5 } },
    },
  };
  const cards = buildBinaryCards(data);
  assert.equal(cards.length, 3);
  const arm = cards.find((c) => c.key === "linux-arm64");
  assert.equal(arm.label, "Linux · ARM64");
  assert.equal(binaryStateLabel(arm.binary).available, false);
  assert.match(arm.binary.error, /arm64/);
});

test("legacy hubs keep the two unlabeled cards", () => {
  const data = { agent_binaries: { linux: { sha: "aaa" }, windows: { sha: "bbb" } } };
  const cards = buildBinaryCards(data);
  assert.deepEqual(cards.map((c) => c.key), ["linux", "windows"]);
});

test("oldest hubs with only hub_sha fields still render legacy cards", () => {
  const cards = buildBinaryCards({ hub_sha: "aaa", hub_windows_sha: "bbb" });
  assert.deepEqual(cards.map((c) => c.key), ["linux", "windows"]);
  assert.equal(cards[0].binary.source, "legacy API");
  assert.equal(buildBinaryCards({}).length, 0);
});

test("agent status is offered-build truth, not 'latest'", () => {
  assert.equal(agentStatusLabel({ update_pending: true }).label, "Update pending");
  assert.equal(agentStatusLabel({ update_pending: true, update_blocked_reason: "x" }).label, "Withheld");
  assert.equal(agentStatusLabel({ update_pending: false, update_blocked_reason: "x" }).label, "Unavailable");
  // Current-hub contract: not pending and not blocked implies the match.
  assert.equal(agentStatusLabel({ update_pending: false, running_sha: "aaa" }, { agent_binaries_by_arch: { linux: { amd64: { sha: "aaa" } } } }).label, "Matches offered build");
  // Older/malformed responses without a running SHA must not go green.
  assert.equal(agentStatusLabel({ update_pending: false, running_sha: "" }).label, "Version unknown");
  assert.equal(agentStatusLabel({ update_pending: false }).label, "Version unknown");
});

test("older hubs never infer a match from no pending update", () => {
  const agent = { os: "linux", running_sha: "aaa", update_pending: false };
  for (const data of [undefined, {}, { hub_sha: "" }, { agent_binaries: { linux: { sha: "" } } }, { hub_sha: "aaa" }]) {
    assert.equal(agentStatusLabel(agent, data).kind, "unknown");
    assert.equal(agentStatusLabel(agent, data).label, "Status unknown (older hub)");
  }
});

test("missing platform entries never throw", () => {
  assert.equal(binaryStateLabel(undefined).available, false);
  assert.equal(binaryStateLabel(null).label, "Unavailable");
  const cards = buildBinaryCards({ agent_binaries: { linux: { sha: "aaa" } } });
  assert.equal(cards.length, 2);
  assert.equal(binaryStateLabel(cards[1].binary).available, false); // windows absent
});

test("protocol notes flag the rollback-floor limitation honestly", () => {
  assert.equal(agentProtocolNote({ update_protocol: 2 }), null);
  assert.equal(agentProtocolNote({ update_protocol: 1 }), "No rollback floor (protocol 1)");
  assert.equal(agentProtocolNote({ update_protocol: 0 }), "Pre-signature agent (no update verification)");
  // Field absent on older hubs: unknown, not protocol 0.
  assert.equal(agentProtocolNote({}), "Protocol unknown (older hub)");
});
