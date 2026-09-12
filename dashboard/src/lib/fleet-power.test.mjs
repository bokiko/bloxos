import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  coverageSentence,
  domainOf,
  fleetPowerChartRows,
  domainSeries,
  combinedSeries,
  currentReading,
  normalizeFleetPowerCurrent,
  resolveDomainChoice,
  availableDomains,
  fleetPowerWarnings,
  formatKWh,
  formatMoney,
  formatWatts,
  normalizeFleetPower,
  shortfallSentence,
} from "./fleet-power.mjs";

/* ============================================================================
 * The fleet power pane's whole job is to not lie about a partial reading.
 * These tests pin the three ways it could: merging a measurement with a model,
 * presenting a partial sum as a fleet total, and drawing a line across time
 * nobody observed.
 * ========================================================================== */

const T0 = 1_700_000_000_000;

function bucket(index, over = {}) {
  return {
    start_unix_ms: T0 + index * 60_000,
    end_unix_ms: T0 + (index + 1) * 60_000,
    measured_watts: null,
    measured_machines: 0,
    estimated_watts: null,
    estimated_machines: 0,
    gap: false,
    ...over,
  };
}

function kind(over = {}) {
  return { machines: 0, energy_kwh: 0, observed_machine_seconds: 0, sources: [], ...over };
}

function domain(name, over = {}) {
  return {
    domain: name,
    buckets: [],
    measured: kind(),
    estimated: kind(),
    reporting_machines: 0,
    complete: false,
    ...over,
  };
}

function response(over = {}) {
  return {
    period: "1h",
    bucket_seconds: 60,
    start_unix_ms: T0,
    end_unix_ms: T0 + 3 * 60_000,
    domains: [domain("system"), domain("cpu"), domain("dram"), domain("gpu")],
    coverage: {
      machines_total: 6,
      machines_reporting: 0,
      machines_silent: 6,
      silent_machine_ids: [],
      machines_api_polled: 0,
      degraded_machines: 0,
      gaps_declared: 0,
      gap_buckets: 0,
      truncated: false,
    },
    ...over,
  };
}

/** A fleet where 3 machines measure whole-machine power and 1 models it. */
function mixedFleet() {
  return normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 4,
          measured: kind({ machines: 3, energy_kwh: 0.71, observed_machine_seconds: 10_800, sources: ["rapl-psys", "ipmi-dcmi"] }),
          estimated: kind({ machines: 1, energy_kwh: 0.09, observed_machine_seconds: 3_600, sources: ["estimate-util"] }),
          buckets: [
            bucket(0, { measured_watts: 140, measured_machines: 3, estimated_watts: 17, estimated_machines: 1 }),
            bucket(1, { measured_watts: 142, measured_machines: 3, estimated_watts: 18, estimated_machines: 1 }),
            bucket(2),
          ],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
      coverage: {
        machines_total: 6,
        machines_reporting: 4,
        machines_silent: 2,
        silent_machine_ids: ["m5", "m6"],
        machines_api_polled: 1,
        degraded_machines: 0,
        gaps_declared: 0,
        gap_buckets: 0,
        truncated: false,
      },
    }),
  );
}

test("measured and estimated stay two numbers, never one", () => {
  const history = mixedFleet();
  const system = domainOf(history, "system");

  assert.equal(system.buckets[1].measured, 142);
  assert.equal(system.buckets[1].estimated, 18);
  // Nothing in the module produces 160. The two series are addressed
  // separately all the way to the chart rows.
  const series = domainSeries(history, "system");
  assert.deepEqual(
    series.map((s) => s.kind),
    ["measured", "estimated"],
  );
  const rows = fleetPowerChartRows(history, series);
  assert.equal(rows[1]["system:measured"], 142);
  assert.equal(rows[1]["system:estimated"], 18);
  assert.ok(!Object.keys(rows[1]).some((k) => k === "total" || k === "watts"));

  // Each series is labelled in text; a reader is never asked to tell them
  // apart by stroke colour.
  assert.deepEqual(
    series.map((s) => s.label),
    ["Measured", "Modelled"],
  );
});

test("a fleet with no estimates charts one line, not an empty second one", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 2,
          measured: kind({ machines: 2, energy_kwh: 0.4, observed_machine_seconds: 7200 }),
          buckets: [bucket(0, { measured_watts: 200, measured_machines: 2 })],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  const series = domainSeries(history, "system");
  assert.equal(series.length, 1);
  assert.equal(series[0].kind, "measured");
});

test("with no whole-machine counter anywhere, components are charted separately", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system"),
        domain("cpu", {
          reporting_machines: 3,
          measured: kind({ machines: 3, energy_kwh: 0.2 }),
          buckets: [bucket(0, { measured_watts: 180, measured_machines: 3 })],
        }),
        domain("dram"),
        domain("gpu", {
          reporting_machines: 2,
          measured: kind({ machines: 2, energy_kwh: 0.5 }),
          buckets: [bucket(0, { measured_watts: 420, measured_machines: 2 })],
        }),
      ],
    }),
  );
  // With no whole-machine counter, the default lands on a MEASURED component
  // domain rather than an empty system one.
  assert.equal(resolveDomainChoice(history, null), "cpu");
  const series = [...domainSeries(history, "cpu"), ...domainSeries(history, "gpu")];
  assert.deepEqual(
    series.map((s) => s.domain),
    ["cpu", "gpu"],
  );
  // Two lines, two labels, no 600 W wall-power claim assembled from them.
  const rows = fleetPowerChartRows(history, series);
  assert.equal(rows[0]["cpu:measured"], 180);
  assert.equal(rows[0]["gpu:measured"], 420);
  assert.equal(rows.length, 1);
});

test("one machine reporting system power does NOT take over the chart", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 1,
          measured: kind({ machines: 1 }),
          buckets: [bucket(0, { measured_watts: 90, measured_machines: 1 })],
        }),
        domain("cpu", {
          reporting_machines: 5,
          measured: kind({ machines: 5 }),
          buckets: [bucket(0, { measured_watts: 300, measured_machines: 5 })],
        }),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  // The old rule handed the whole chart to `system` whenever ANY machine had
  // it, hiding the five machines reporting CPU. Worse, once a MODELLED system
  // reading could appear, one estimating board would have evicted the measured
  // history of the entire fleet.
  //
  // Every domain with data is now offered, and the default prefers a measured
  // component domain over a one-machine system reading.
  assert.deepEqual(availableDomains(history), ["system", "cpu"]);
  assert.equal(resolveDomainChoice(history, null), "cpu");
  // An explicit choice is always honoured.
  assert.equal(resolveDomainChoice(history, "system"), "system");
  // Even for a domain with no data at all: a reader watching DRAM is asking to
  // watch DRAM, and moving them would hide the very fact they selected it for.
  assert.equal(resolveDomainChoice(history, "dram"), "dram");
});

test("an empty response offers no domains and charts no lines", () => {
  const empty = normalizeFleetPower(response());
  assert.deepEqual(availableDomains(empty), []);
  assert.deepEqual(domainSeries(empty, "system"), []);
  // A deterministic fallback, so the shape never depends on what arrived.
  assert.equal(resolveDomainChoice(empty, null), "cpu");
});

test("a bucket nobody covered stays null so the line breaks", () => {
  const history = mixedFleet();
  const series = domainSeries(history, "system");
  const rows = fleetPowerChartRows(history, series);
  assert.equal(rows.length, 3);
  assert.equal(rows[2]["system:measured"], null, "an unobserved bucket is not zero watts");
  assert.equal(rows[2]["system:estimated"], null);
});

test("coverage never says 'all' unless every machine reported", () => {
  assert.equal(coverageSentence(mixedFleet().coverage), "4 of 6 machines reporting power.");
  assert.equal(
    coverageSentence({ machinesTotal: 3, machinesReporting: 3 }),
    "All 3 machines are reporting power.",
  );
  assert.equal(
    coverageSentence({ machinesTotal: 1, machinesReporting: 1 }),
    "All 1 machine is reporting power.",
  );
  assert.equal(
    coverageSentence({ machinesTotal: 6, machinesReporting: 0 }),
    "No power readings from any of 6 machines.",
  );
  assert.equal(coverageSentence({ machinesTotal: 0, machinesReporting: 0 }), "No machines in this fleet.");
});

test("the shortfall separates 'cannot report' from 'is not reporting'", () => {
  const c = mixedFleet().coverage;
  assert.equal(
    shortfallSentence(c),
    "1 API-polled (no agent, so no power counter) · 1 with no power counter reporting",
  );
  assert.equal(shortfallSentence({ machinesTotal: 3, machinesReporting: 3, machinesAPIPolled: 0 }), null);
  assert.equal(
    shortfallSentence({ machinesTotal: 4, machinesReporting: 2, machinesAPIPolled: 9 }),
    "2 API-polled (no agent, so no power counter)",
    "an API-polled count larger than the shortfall cannot invent extra missing machines",
  );
});

test("a partial fleet is never marked complete", () => {
  const history = mixedFleet();
  assert.equal(domainOf(history, "system").complete, false);
  assert.equal(domainOf(history, "system").reportingMachines, 4);
  assert.equal(history.coverage.machinesTotal, 6);
});

test("gaps, degradation and truncation all reach the reader", () => {
  const history = normalizeFleetPower(
    response({
      coverage: {
        machines_total: 3,
        machines_reporting: 3,
        machines_silent: 0,
        silent_machine_ids: [],
        machines_api_polled: 0,
        degraded_machines: 2,
        gaps_declared: 1,
        gap_buckets: 4,
        truncated: true,
      },
    }),
  );
  const warnings = fleetPowerWarnings(history);
  assert.equal(warnings.length, 3);
  assert.match(warnings[0], /Missing data is not zero power/);
  assert.match(warnings[1], /2 machines are recording power with reduced coverage/);
  assert.match(warnings[2], /oldest part of this window is missing/);
  assert.deepEqual(fleetPowerWarnings(normalizeFleetPower(response())), []);
});

test("a gapped bucket marks its chart row", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 1,
          measured: kind({ machines: 1 }),
          buckets: [
            bucket(0, { measured_watts: 100, measured_machines: 1 }),
            bucket(1, { measured_watts: 100, measured_machines: 1, gap: true }),
          ],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  const rows = fleetPowerChartRows(history, domainSeries(history, "system"));
  assert.equal(rows[0].gap, false);
  assert.equal(rows[1].gap, true);
  assert.equal(rows[1]["system:measured"], 100, "a gap flag does not blank the reading beside it");
});

/* --- cost --- */

test("a response that is not fleet power is refused, not half-rendered", () => {
  for (const junk of [null, undefined, 0, "", [], {}, { domains: [] }, { coverage: {} }]) {
    assert.throws(() => normalizeFleetPower(junk), /Invalid fleet power response/);
  }
});

test("junk inside a valid envelope degrades field by field", () => {
  const history = normalizeFleetPower(
    response({
      period: "7d",
      domains: [
        domain("system", {
          reporting_machines: "lots",
          measured: kind({ machines: 2, energy_kwh: "free", sources: ["rapl-psys", 7, ""] }),
          buckets: [
            bucket(0, { measured_watts: -5, measured_machines: 2 }),
            bucket(1, { measured_watts: Number.NaN, measured_machines: 1 }),
            { end_unix_ms: "soon", measured_watts: 100 },
          ],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  const system = domainOf(history, "system");
  assert.equal(history.period, "6h", "an unsupported period falls back rather than labelling an axis wrong");
  assert.equal(system.reportingMachines, 0);
  assert.equal(system.measured.energyKWh, 0);
  assert.deepEqual(system.measured.sources, ["rapl-psys"]);
  assert.equal(system.buckets.length, 2, "a bucket with no usable timestamp is dropped");
  assert.equal(system.buckets[0].measured, null, "negative watts are not a reading");
  assert.equal(system.buckets[1].measured, null, "NaN is not a reading");
});

test("a domain the hub did not send reads as empty, not as a crash", () => {
  const history = normalizeFleetPower(response({ domains: [domain("system")] }));
  const gpu = domainOf(history, "gpu");
  assert.equal(gpu.reportingMachines, 0);
  assert.deepEqual(gpu.buckets, []);
  assert.equal(gpu.measured.energyKWh, 0);
});

/* --- formatting --- */

test("formatting keeps small numbers legible and absent ones absent", () => {
  assert.equal(formatWatts(null), "—");
  assert.equal(formatWatts(undefined), "—");
  assert.equal(formatWatts(142.4), "142 W");
  assert.equal(formatWatts(18.25), "18.3 W");
  assert.equal(formatWatts(1.5), "1.50 W");
  assert.equal(formatKWh(0.0084), "0.008 kWh");
  assert.equal(formatKWh(12.34), "12.3 kWh");
  assert.equal(formatKWh(null), "—");
  assert.equal(formatMoney(null, "USD"), "—");
  // A cost below a cent must not round to a confident zero.
  assert.match(formatMoney(0.0042, "USD"), /0\.0042/);
  assert.match(formatMoney(9.99, "USD"), /9\.99/);
  // A currency Intl rejects degrades rather than throwing mid-render.
  assert.match(formatMoney(1.5, "NOT-A-CODE"), /1\.5000 NOT-A-CODE/);
});

/* --- the pane's contract with the page ---------------------------------- */

const PANE = readFileSync(new URL("../components/overview/FleetPowerPane.tsx", import.meta.url), "utf8");
const PAGE = readFileSync(new URL("../app/page.tsx", import.meta.url), "utf8");

const WORKSPACE = readFileSync(new URL("../components/overview/OverviewWorkspace.tsx", import.meta.url), "utf8");

test("the pane is mounted once, by the component that owns the arrangement", () => {
  // The Overview no longer folds anything: power is the anchor and is always
  // visible, so the pane is a plain panel and the page composes a workspace
  // rather than a fixed pane row.
  assert.equal(count(WORKSPACE, /<FleetPowerPane\b/g), 1, "the workspace mounts the pane exactly once");
  assert.equal(count(PAGE, /<OverviewWorkspace\b/g), 1, "the page mounts the workspace exactly once");
  assert.doesNotMatch(PAGE, /<FleetPowerPane\b/, "the page composes the workspace, not the pane");
  assert.doesNotMatch(PAGE + WORKSPACE, /mf-pane-grid/, "the fixed two-column pane row is gone");
  assert.doesNotMatch(PAGE, /id="fleet"/, "the machine table is a work surface, not a disclosure");

  // The panes this Overview shed stay shed. Unimported is not enough: an
  // orphaned pane file is how the last four-panel layout kept coming back.
  assert.doesNotMatch(PAGE, /CapacityPane|AttentionPanel|HighestLoadPane/);
});

test("the pane draws flat lines in tokens, with no invented decoration", () => {
  // Every colour resolves to a --mf-* / semantic token; no raw palette values.
  assert.doesNotMatch(PANE, /#[0-9a-fA-F]{3,8}\b/, "no hardcoded hex colours");
  for (const banned of [
    /<Area\b/,
    /AreaChart/,
    /linearGradient/,
    /radialGradient/,
    /boxShadow/,
    /filter:\s*['"`]?(?:drop-)?shadow/,
    /backdrop-blur/,
    /ReferenceLine/,
  ]) {
    assert.doesNotMatch(PANE, banned, `decoration ${banned} does not belong in this pane`);
  }
  // Gaps break the line rather than being drawn across.
  assert.match(PANE, /connectNulls=\{false\}/);
  assert.match(PANE, /isAnimationActive=\{false\}/);
});

test("estimated readings are labelled in words wherever they appear", () => {
  // The guard that matters: an estimate must never be distinguishable only by
  // its stroke. Every place the pane renders one, it also writes it down.
  assert.match(PANE, /modelled, not measured/);
  assert.match(PANE, /\(modelled\)/, "the chart legend/tooltip name says so too");
  assert.match(PANE, /est/, "the folded summary marks its estimate");
  // And nothing in the pane adds the two together.
  assert.doesNotMatch(PANE, /measured\s*\+\s*estimated|estimated\s*\+\s*measured/);
});

test("coverage stays on the pane after the prose came off it", () => {
  // The pane was cut down to figures: the coverage SENTENCE and the shortfall
  // sentence no longer have a paragraph of their own. They must not have been
  // dropped with it — a partial sum presented as a fleet total is the exact
  // failure this file exists to prevent.
  //
  // Short form, beside the number it qualifies:
  assert.match(PANE, /reporting/, "the visible readout still says how many machines are behind it");
  assert.match(PANE, /machinesReporting/, "and it counts them rather than asserting a total");
  // Long form, still built from the same numbers, for the tooltip and for the
  // chart's accessible name:
  assert.match(PANE, /coverageSentence\(/);
  assert.match(PANE, /shortfallSentence\(/);
  assert.match(PANE, /aria-label=\{`[\s\S]*?coverageDetail\(coverage\)/);
});

test("the tariff is set in Settings and only displayed on the pane", () => {
  const settings = readFileSync(
    new URL("../components/settings/PowerRateSettings.tsx", import.meta.url),
    "utf8",
  );
  const preferences = readFileSync(
    new URL("../components/settings/PreferencesSettings.tsx", import.meta.url),
    "utf8",
  );

  // A rate is typed once and read on every visit, so its control does not
  // belong on the Overview. The pane keeps the cost; it loses the input.
  assert.doesNotMatch(PANE, /<input/, "no field on the pane");
  assert.doesNotMatch(PANE, /<select/, "no currency picker on the pane");
  assert.doesNotMatch(PANE, /onRateChange/, "the pane cannot write the rate at all");
  // Energy and cost are withheld in this release: sampled power cannot be
  // extrapolated into measured energy. The pane must say so rather than print
  // a figure, and must never print a zero in place of a missing accounting.
  assert.doesNotMatch(PANE, /At least/, "the lower-bound claim is withdrawn");
  assert.match(PANE, /Energy and cost unavailable/, "it says what it does not know");
  assert.doesNotMatch(PANE, /formatMoney\(/, "no money figure while accounting is withheld");

  // Both halves of it moved, to the one place that owns it, and it is mounted.
  assert.match(settings, /POWER_CURRENCIES/, "currency moved too, not just the number");
  assert.match(settings, /POWER_MAX_RATE/, "with the same typo ceiling it had on the pane");
  assert.match(settings, /useWorkspacePrefs/, "and into the same store, not a new one");
  assert.match(preferences, /<PowerRateSettings\s*\/>/, "mounted beside the other preferences");
});

function count(source, pattern) {
  return (source.match(pattern) ?? []).length;
}

// --- integration contract ---
//
// These assert the PANE actually uses the honest helpers. Without them a helper
// can be written, tested in isolation, and never wired up — which is exactly
// how the automatic-mode defect survived: the pure functions were fine and the
// component called the wrong ones.

test("the pane drives its domain from explicit selection, not automatic mode", () => {
  assert.match(PANE, /resolveDomainChoice\(/, "the reader's choice resolves the domain");
  assert.match(PANE, /combinedSeries\(/, "series come from history AND the current snapshot");
  assert.doesNotMatch(PANE, /fleetPowerMode\(/, "the automatic mode is gone");
  assert.doesNotMatch(PANE, /fleetPowerSeries\(/, "and so is the series function built on it");
  assert.match(PANE, /aria-label="Power domain"/, "there is a control to change it");
});

test("the pane's current readouts come from the snapshot, never from history", () => {
  assert.match(PANE, /currentReading\(/, "readouts read the current snapshot");
  assert.match(PANE, /api\/fleet\/power\/current/, "which is its own request");
  assert.doesNotMatch(PANE, /latestReading\(/, "the history walk-back must not drive a current value");
});

test("the pane states what its number is, and what is missing from it", () => {
  assert.match(PANE, /sum of sample means/, "the figure is qualified in visible copy");
  assert.match(PANE, /freshnessNote\(/, "a value that is not current carries its age");
  assert.match(PANE, /unrecognised backend/, "unknown provenance is explained, not hidden");
  assert.match(PANE, /Excluded:/, "and so are stale, skewed and unreadable contributors");
});

test("the pane reads stored preferences after mount, never during render", () => {
  // localStorage in a useState initialiser renders differently on the server
  // and in the browser, which React reports as a hydration mismatch.
  assert.doesNotMatch(
    PANE,
    /useState<string \| null>\(\(\) => readStoredDomain\(\)\)/,
    "storage must not be read in the initialiser",
  );
  assert.match(PANE, /useEffect\(\(\) => \{\n\s*if \(latched\.current\) return;/, "it is read in an effect");
});

// A capped or empty history must not hide a live reading. This is behavioural,
// not a source grep: the pane's readouts are built from these series.
test("a current-only measured reading still produces a series and a readout", () => {
  const emptyHistory = normalizeFleetPower(response());
  const snapshot = normalizeFleetPowerCurrent({
    generated_unix_ms: T0,
    machines_total: 4,
    machines_reporting: 1,
    domains: [
      {
        domain: "cpu",
        measured: {
          watts: 100,
          machines: 1,
          sources: ["rapl-package"],
          oldest_contributor_end_unix_ms: T0,
          newest_contributor_end_unix_ms: T0,
        },
      },
    ],
  });

  assert.deepEqual(domainSeries(emptyHistory, "cpu"), [], "history alone has nothing");
  const series = combinedSeries(emptyHistory, snapshot, "cpu");
  assert.equal(series.length, 1, "the live reading still earns a series");
  assert.equal(series[0].kind, "measured");

  const reading = currentReading(snapshot, "cpu", "measured", T0);
  assert.equal(reading.watts, 100);
  assert.equal(reading.machines, 1);
});

test("a current-only MODELLED system reading is charted as modelled", () => {
  const emptyHistory = normalizeFleetPower(response());
  const snapshot = normalizeFleetPowerCurrent({
    generated_unix_ms: T0,
    machines_total: 4,
    machines_reporting: 1,
    domains: [
      {
        domain: "system",
        estimated: {
          watts: 12,
          machines: 1,
          sources: ["estimate-util"],
          oldest_contributor_end_unix_ms: T0,
          newest_contributor_end_unix_ms: T0,
        },
      },
    ],
  });
  const series = combinedSeries(emptyHistory, snapshot, "system");
  assert.equal(series.length, 1);
  assert.equal(series[0].kind, "estimated");
  assert.equal(series[0].label, "Modelled");
  assert.equal(currentReading(snapshot, "system", "measured", T0), null, "no measurement to report");
  assert.equal(currentReading(snapshot, "system", "estimated", T0).watts, 12);
});

test("an unrecognised kind is rejected rather than read as modelled", () => {
  const snapshot = normalizeFleetPowerCurrent({
    generated_unix_ms: T0,
    domains: [
      {
        domain: "cpu",
        estimated: {
          watts: 5,
          machines: 1,
          sources: ["estimate-util"],
          oldest_contributor_end_unix_ms: T0,
          newest_contributor_end_unix_ms: T0,
        },
      },
    ],
  });
  assert.equal(currentReading(snapshot, "cpu", "unknown", T0), null);
  assert.equal(currentReading(snapshot, "cpu", "", T0), null);
});

test("a real measured zero survives; unavailable is null, not zero", () => {
  const snapshot = normalizeFleetPowerCurrent({
    generated_unix_ms: T0,
    domains: [
      {
        domain: "dram",
        measured: {
          watts: 0,
          machines: 1,
          sources: ["rapl-dram"],
          oldest_contributor_end_unix_ms: T0,
          newest_contributor_end_unix_ms: T0,
        },
      },
    ],
  });
  const reading = currentReading(snapshot, "dram", "measured", T0);
  assert.equal(reading.watts, 0, "a machine can genuinely measure zero on a domain");
  // Nothing reporting at all is a different answer entirely.
  const empty = normalizeFleetPowerCurrent({ generated_unix_ms: T0, domains: [] });
  assert.equal(currentReading(empty, "dram", "measured", T0), null);
});

test("a domain whose only contributors are unclassified is still selectable", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system"),
        domain("cpu"),
        domain("dram", { unknown: kind({ machines: 1, sources: ["some-future-backend"] }) }),
        domain("gpu"),
      ],
    }),
  );
  // It charts nothing, by design — but a reader must be able to open it and
  // find out WHY it is empty.
  assert.ok(availableDomains(history).includes("dram"));
  assert.deepEqual(domainSeries(history, "dram"), []);
  assert.equal(domainOf(history, "dram").unknown.machines, 1);
});
