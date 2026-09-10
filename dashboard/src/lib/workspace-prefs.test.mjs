import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_WORKSPACE_PREFS,
  LOAD_METRICS,
  OVERVIEW_SECTIONS,
  normalizeWorkspacePrefs,
  readWorkspacePrefs,
  toggleMember,
  workspacePrefsKey,
  writeWorkspacePrefs,
} from "./workspace-prefs.mjs";

test("keys are scoped per user, like the preferences cache", () => {
  assert.notEqual(workspacePrefsKey("u1"), workspacePrefsKey("u2"));
  // A logged-out reader gets a key of its own rather than the last user's.
  assert.equal(workspacePrefsKey(null), workspacePrefsKey(undefined));
  assert.notEqual(workspacePrefsKey(null), workspacePrefsKey("u1"));
});

test("the defaults are the behaviour that shipped", () => {
  const d = normalizeWorkspacePrefs(null);
  assert.deepEqual(d.collapsed, [], "nothing starts collapsed");
  assert.equal(d.load_metric, "cpu", "the load ranking still defaults to CPU");
  assert.deepEqual(d.expected_high_cpu, []);
  assert.equal(DEFAULT_WORKSPACE_PREFS.load_metric, "cpu");
});

test("garbage in storage degrades to defaults instead of throwing", () => {
  for (const junk of [undefined, 0, "", "nope", [], { collapsed: "all" }]) {
    const out = normalizeWorkspacePrefs(junk);
    assert.deepEqual(out.collapsed, []);
    assert.equal(out.load_metric, "cpu");
    assert.deepEqual(out.expected_high_cpu, []);
  }
});

test("unknown section ids and metrics are dropped, not stored", () => {
  const out = normalizeWorkspacePrefs({
    collapsed: ["fleet", "not-a-section", "", 7, "fleet"],
    load_metric: "vibes",
  });
  assert.deepEqual(out.collapsed, ["fleet"], "unknown, empty and duplicate ids all go");
  assert.equal(out.load_metric, "cpu");
  for (const id of out.collapsed) assert.ok(OVERVIEW_SECTIONS.includes(id));
});

test("every declared section and metric survives a round trip", () => {
  const out = normalizeWorkspacePrefs({
    collapsed: [...OVERVIEW_SECTIONS],
    load_metric: "memory",
  });
  assert.deepEqual(out.collapsed, [...OVERVIEW_SECTIONS]);
  assert.equal(out.load_metric, "memory");
  for (const m of LOAD_METRICS) {
    assert.equal(normalizeWorkspacePrefs({ load_metric: m }).load_metric, m);
  }
});

test("machine ids are kept opaque and are not validated against a fleet", () => {
  // A flagged machine that drops off the SSE stream for a minute must not lose
  // its flag, so ids are never reconciled here.
  const out = normalizeWorkspacePrefs({
    expected_high_cpu: ["orangepi5-01", "api-7", "", null, "orangepi5-01"],
  });
  assert.deepEqual(out.expected_high_cpu, ["orangepi5-01", "api-7"]);
});

test("toggleMember adds, removes, and never mutates its input", () => {
  const start = ["a"];
  assert.deepEqual(toggleMember(start, "b"), ["a", "b"]);
  assert.deepEqual(toggleMember(start, "a"), []);
  assert.deepEqual(start, ["a"], "the caller's array is untouched");
  assert.deepEqual(toggleMember(undefined, "a"), ["a"]);
});

test("storage access never throws at the caller", () => {
  // No window here (node:test), which is the same path a server render takes:
  // reads return defaults and writes are no-ops, rather than blowing up.
  assert.deepEqual(readWorkspacePrefs("u1"), normalizeWorkspacePrefs(null));
  assert.doesNotThrow(() => writeWorkspacePrefs("u1", { collapsed: ["fleet"] }));
});

test("a logged-out reader reads nothing", () => {
  assert.deepEqual(readWorkspacePrefs(null), normalizeWorkspacePrefs(null));
});
