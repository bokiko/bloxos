import test from "node:test";
import assert from "node:assert/strict";

import {
  POWER_FRESH_MS,
  FRESH,
  STALE,
  SKEWED,
  UNAVAILABLE,
  freshnessOf,
  aggregateFreshness,
  isCurrent,
  formatAge,
  freshnessNote,
} from "./power-freshness.mjs";

const NOW = 1_700_000_000_000;

test("a recent observation is current", () => {
  const f = freshnessOf(NOW - 30_000, NOW);
  assert.equal(f.state, FRESH);
  assert.ok(isCurrent(f));
});

test("an old observation is stale and carries its age", () => {
  const f = freshnessOf(NOW - 4 * 3_600_000, NOW);
  assert.equal(f.state, STALE);
  assert.equal(isCurrent(f), false);
  assert.equal(formatAge(f.ageMS), "4h");
  assert.match(freshnessNote(f), /4h ago/);
});

test("nothing ever reported is unavailable, not zero-aged", () => {
  assert.equal(freshnessOf(null, NOW).state, UNAVAILABLE);
  assert.equal(freshnessOf(0, NOW).state, UNAVAILABLE);
  assert.equal(formatAge(freshnessOf(null, NOW).ageMS), null);
});

// A reading cannot be newer than now. A clock running ahead produces an
// observation "in the future"; that is unknown age, never current.
test("a future observation is skewed, never fresh", () => {
  const modest = freshnessOf(NOW + 30_000, NOW);
  assert.equal(modest.state, SKEWED, "30s ahead is skew, not a current reading");
  assert.equal(isCurrent(modest), false);

  const nearOldGrace = freshnessOf(NOW + 119_000, NOW);
  assert.equal(nearOldGrace.state, SKEWED, "119s ahead sat inside the old two-minute grace");
  assert.equal(isCurrent(nearOldGrace), false);

  assert.match(freshnessNote(nearOldGrace), /clock ahead/);
  // Skew must not be printed as an age.
  assert.equal(formatAge(nearOldGrace.ageMS), null);
});

test("timestamp jitter inside tolerance is still current", () => {
  assert.equal(freshnessOf(NOW + 500, NOW).state, FRESH);
});

// The rule that stops one machine speaking for a dark fleet.
test("an aggregate is only as fresh as its OLDEST contributor", () => {
  const oneFreshFiveDark = [
    NOW - 10_000,
    NOW - 3_600_000,
    NOW - 3_600_000,
    NOW - 3_600_000,
    NOW - 3_600_000,
    NOW - 3_600_000,
  ];
  const f = aggregateFreshness(oneFreshFiveDark, NOW);
  assert.equal(f.state, STALE, "one still-reporting machine must not launder five stale ones");
  assert.equal(f.contributors, 6);
  assert.equal(formatAge(f.ageMS), "1h");
});

test("an aggregate whose contributors are all recent is current", () => {
  const f = aggregateFreshness([NOW - 10_000, NOW - 20_000], NOW);
  assert.equal(f.state, FRESH);
  assert.ok(isCurrent(f));
});

test("one skewed contributor makes the whole aggregate's age unknowable", () => {
  const f = aggregateFreshness([NOW - 10_000, NOW + 600_000], NOW);
  assert.equal(f.state, SKEWED);
  assert.equal(isCurrent(f), false);
});

test("an aggregate with no contributors is unavailable", () => {
  assert.equal(aggregateFreshness([], NOW).state, UNAVAILABLE);
  assert.equal(aggregateFreshness(null, NOW).state, UNAVAILABLE);
  assert.equal(aggregateFreshness([0, null, NaN], NOW).state, UNAVAILABLE);
});

test("the freshness window is a single shared constant", () => {
  // Three different rules used to coexist (90s, 150s, and a 24h walk-back).
  // The exact value matters less than there being exactly one.
  assert.equal(typeof POWER_FRESH_MS, "number");
  assert.ok(POWER_FRESH_MS > 0);
  assert.equal(freshnessOf(NOW - (POWER_FRESH_MS - 1), NOW).state, FRESH);
  assert.equal(freshnessOf(NOW - (POWER_FRESH_MS + 1), NOW).state, STALE);
});
