import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_WORKSPACE_PREFS,
  POWER_CURRENCIES,
  POWER_MAX_RATE,
  POWER_PERIODS,
  normalizePowerRate,
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
  assert.deepEqual(d.expected_high_cpu, []);
  assert.equal(DEFAULT_WORKSPACE_PREFS.power_period, "6h");
});

test("garbage in storage degrades to defaults instead of throwing", () => {
  for (const junk of [undefined, 0, "", "nope", [], { collapsed: "all" }]) {
    const out = normalizeWorkspacePrefs(junk);
    assert.deepEqual(out.expected_high_cpu, []);
    assert.equal(out.power_period, DEFAULT_WORKSPACE_PREFS.power_period);
  }
});

test("a collapse state from the folding era is dropped, not fatal", () => {
  // The Overview no longer folds anything — power is always visible and the
  // machine table is a work surface, not a disclosure. Every operator who ever
  // folded a pane still has those ids in localStorage, so the normalizer has
  // to read them, ignore them, and never write them back. Throwing or
  // preserving them would break a workspace over a preference nothing reads.
  const out = normalizeWorkspacePrefs({
    collapsed: ["availability", "capacity", "fleet", "attention", "load"],
    power_period: "24h",
  });
  assert.ok(!("collapsed" in out), "the retired key must not survive a read");
  assert.equal(out.power_period, "24h", "the rest of the workspace is untouched");
  assert.deepEqual(normalizeWorkspacePrefs(out).power_period, "24h");
});

test("the retired load-ranking metric is gone from the schema entirely", () => {
  // The pane it drove was removed; a field nothing reads is a field that
  // rots. A stored value is inert rather than an error.
  assert.ok(!("load_metric" in DEFAULT_WORKSPACE_PREFS));
  assert.ok(!("load_metric" in normalizeWorkspacePrefs({ load_metric: "gpu" })));
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

test("there is no default electricity tariff", () => {
  // A "typical" rate would print a confident, specific, invented cost beside
  // real watts. The pane asks for one instead.
  assert.equal(DEFAULT_WORKSPACE_PREFS.power_rate.per_kwh, null);
  assert.equal(normalizeWorkspacePrefs(null).power_rate.per_kwh, null);
  assert.equal(normalizeWorkspacePrefs(null).power_rate.currency, "USD");
  assert.equal(normalizeWorkspacePrefs(null).power_period, "6h");
  assert.ok(POWER_PERIODS.includes(DEFAULT_WORKSPACE_PREFS.power_period));
});

test("a stated tariff round-trips, including a free one", () => {
  for (const per_kwh of [0, 0.0001, 0.12, 0.285, POWER_MAX_RATE]) {
    assert.equal(normalizePowerRate({ currency: "EUR", per_kwh }).per_kwh, per_kwh);
  }
  assert.equal(normalizePowerRate({ currency: "EUR", per_kwh: 0.28 }).currency, "EUR");
  for (const currency of POWER_CURRENCIES) {
    assert.equal(normalizePowerRate({ currency, per_kwh: 0.1 }).currency, currency);
  }
});

test("an unusable tariff becomes absent, never zero", () => {
  // Zero means "my power is free", so an unparseable rate must not become it.
  for (const per_kwh of [undefined, null, "0.28", Number.NaN, Infinity, -1, POWER_MAX_RATE + 1, {}]) {
    assert.equal(normalizePowerRate({ currency: "EUR", per_kwh }).per_kwh, null);
  }
  for (const junk of [null, undefined, 0, "", [], "nope"]) {
    assert.deepEqual(normalizePowerRate(junk), { currency: "USD", per_kwh: null });
  }
  // An unknown currency code falls back rather than reaching Intl and throwing.
  assert.equal(normalizePowerRate({ currency: "XYZ", per_kwh: 0.1 }).currency, "USD");
});

test("an unknown power period falls back to the default", () => {
  assert.equal(normalizeWorkspacePrefs({ power_period: "7d" }).power_period, "6h");
  assert.equal(normalizeWorkspacePrefs({ power_period: 24 }).power_period, "6h");
  for (const period of POWER_PERIODS) {
    assert.equal(normalizeWorkspacePrefs({ power_period: period }).power_period, period);
  }
});
