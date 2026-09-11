import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  DEFAULT_OVERVIEW_WIDGETS,
  OVERVIEW_WIDGET_KEYS,
  alertBreakdown,
  healthLine,
  healthTone,
  isDefaultOverview,
  isOverviewLayout,
  normalizeOverviewWidgets,
  urgentAlert,
  visibleOverviewModules,
} from "./overview-layout.mjs";

const alert = (id, severity = "warning") => ({ id, severity, machine_id: `m-${id}`, message: "x" });
const counts = (over = {}) => ({ total: 6, live: 6, warning: 0, critical: 0, stale: 0, offline: 0, needsReview: 0, connected: 6, ...over });

test("Highest load is not a module this product has", () => {
  // It was built once and deliberately removed: four rows of six restated the
  // table directly below them. The key must not reappear in the contract.
  assert.deepEqual([...OVERVIEW_WIDGET_KEYS], ["availability", "attention", "urgent_alert"]);
  assert.ok(!("highest_load" in DEFAULT_OVERVIEW_WIDGETS));
  assert.ok(!("highest_load" in normalizeOverviewWidgets({ highest_load: true })));
});

test("an unrecognisable arrangement is refused, not coerced", () => {
  for (const good of ["machine-first", "balanced", "power-focus"]) assert.ok(isOverviewLayout(good));
  // "classic" and the retired dashboard_layout vocabulary are not arrangements.
  for (const bad of ["classic", "console", "wall", "", null, undefined, 1, {}]) {
    assert.equal(isOverviewLayout(bad), false, `${String(bad)} must not be a layout`);
  }
});

test("a corrupt stored value becomes the recommended set, not an empty overview", () => {
  for (const junk of [null, undefined, "", 7, [], "availability"]) {
    assert.deepEqual(normalizeOverviewWidgets(junk), { ...DEFAULT_OVERVIEW_WIDGETS });
  }
  // A partial object keeps the defaults for what it does not mention, and an
  // explicit false is honoured.
  assert.deepEqual(normalizeOverviewWidgets({ attention: false }),
    { availability: true, attention: false, urgent_alert: true });
});

test("reset-to-recommended knows when there is nothing to reset", () => {
  assert.equal(isDefaultOverview(DEFAULT_OVERVIEW_LAYOUT, DEFAULT_OVERVIEW_WIDGETS), true);
  assert.equal(isDefaultOverview("balanced", DEFAULT_OVERVIEW_WIDGETS), false);
  assert.equal(isDefaultOverview(DEFAULT_OVERVIEW_LAYOUT, { ...DEFAULT_OVERVIEW_WIDGETS, attention: false }), false);
});

test("the urgent alert is the worst one, then the newest", () => {
  // The hub sends active alerts newest-first, so array order is the tiebreak.
  assert.equal(urgentAlert([alert("a"), alert("b")]).id, "a", "all-warning picks the newest");
  assert.equal(urgentAlert([alert("a"), alert("b", "critical")]).id, "b", "critical outranks a newer warning");
  assert.equal(urgentAlert([alert("a", "critical"), alert("b", "critical")]).id, "a", "newest of the criticals");
  // Nothing real to name.
  assert.equal(urgentAlert([]), null);
  assert.equal(urgentAlert(undefined), null);
  assert.equal(urgentAlert([{ severity: "critical" }]), null, "an alert with no id is not an alert");
});

test("the attention breakdown counts only the two severities the hub emits", () => {
  assert.deepEqual(alertBreakdown([alert("a"), alert("b", "critical"), alert("c")]),
    { total: 3, critical: 1, warning: 2 });
  assert.deepEqual(alertBreakdown([]), { total: 0, critical: 0, warning: 0 });
  assert.deepEqual(alertBreakdown(null), { total: 0, critical: 0, warning: 0 });
});

test("the grid is sized by what renders, never by what was selected", () => {
  const ready = { ready: true, alerts: [alert("a")] };

  assert.deepEqual(visibleOverviewModules("machine-first", DEFAULT_OVERVIEW_WIDGETS, ready),
    ["availability", "attention", "urgent_alert"]);

  // Selected but with no honest data: the urgent module is absent, so the
  // stack is 2 and the grid closes up rather than leaving a hole.
  assert.deepEqual(visibleOverviewModules("machine-first", DEFAULT_OVERVIEW_WIDGETS, { ready: true, alerts: [] }),
    ["availability", "attention"]);

  // Turned off one at a time.
  assert.deepEqual(
    visibleOverviewModules("machine-first", { availability: false, attention: true, urgent_alert: false }, ready),
    ["attention"],
  );
  assert.deepEqual(
    visibleOverviewModules("machine-first", { availability: false, attention: false, urgent_alert: false }, ready),
    [],
  );

  // Power focus shows no modules at all — the critical marker it still owes
  // the operator is the workspace's job, not a module's.
  assert.deepEqual(visibleOverviewModules("power-focus", DEFAULT_OVERVIEW_WIDGETS, ready), []);

  // Before the first fleet payload, even the counts would be a guess.
  assert.deepEqual(visibleOverviewModules("machine-first", DEFAULT_OVERVIEW_WIDGETS, { ready: false, alerts: [alert("a")] }), []);
  assert.deepEqual(visibleOverviewModules("balanced", DEFAULT_OVERVIEW_WIDGETS, undefined), []);
});

test("the health line is numbers, and only measured ones", () => {
  assert.equal(healthLine(counts()), "All 6 machines live");
  assert.equal(healthLine(counts({ live: 4, needsReview: 1, warning: 1, offline: 1 })),
    "4 of 6 live · 1 needs review · 1 offline");
  assert.equal(healthLine(counts({ live: 4, needsReview: 2, warning: 2 })), "4 of 6 live · 2 need review");
  assert.equal(healthLine(counts({ total: 0, live: 0 })), "No machines enrolled");
  assert.equal(healthLine(undefined), "No machines enrolled");
  // No adjectives, no second clause, no "here is what changed".
  for (const line of [healthLine(counts()), healthLine(counts({ live: 4, offline: 2 }))]) {
    assert.doesNotMatch(line, /healthy|fine|good|changed|\./);
  }
});

test("the health dot follows the worst real state", () => {
  assert.equal(healthTone(counts()), "ok");
  assert.equal(healthTone(counts({ warning: 1 })), "warning");
  assert.equal(healthTone(counts({ stale: 1 })), "warning");
  assert.equal(healthTone(counts({ offline: 1 })), "critical");
  assert.equal(healthTone(counts({ critical: 1 })), "critical");
  assert.equal(healthTone(counts({ total: 0 })), "neutral");
});
