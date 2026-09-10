import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  coverageSentence,
  domainOf,
  fleetPowerChartRows,
  fleetPowerCost,
  fleetPowerMode,
  fleetPowerSeries,
  fleetPowerWarnings,
  formatKWh,
  formatMoney,
  formatWatts,
  latestReading,
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
  const series = fleetPowerSeries(history);
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
    ["Measured", "Estimated"],
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
  const series = fleetPowerSeries(history);
  assert.equal(series.length, 1);
  assert.equal(series[0].kind, "measured");
});

test("an all-estimated fleet is charted, and never as a measurement", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 2,
          estimated: kind({ machines: 2, energy_kwh: 0.2, observed_machine_seconds: 7200, sources: ["estimate-util"] }),
          buckets: [bucket(0, { estimated_watts: 36, estimated_machines: 2 })],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  const series = fleetPowerSeries(history);
  assert.equal(series.length, 1);
  assert.equal(series[0].kind, "estimated");
  assert.equal(series[0].label, "Estimated");
  assert.equal(latestReading(history, "system", "measured"), null, "there is no measurement to report");
  assert.equal(latestReading(history, "system", "estimated").watts, 36);
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
  assert.equal(fleetPowerMode(history), "component");
  const series = fleetPowerSeries(history);
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

test("system wins over components whenever any machine has a real counter", () => {
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
  assert.equal(fleetPowerMode(history), "system");
});

test("an empty response is a mode of its own, not an empty chart", () => {
  assert.equal(fleetPowerMode(normalizeFleetPower(response())), "none");
  assert.equal(fleetPowerMode(null), "none");
  assert.deepEqual(fleetPowerSeries(normalizeFleetPower(response())), []);
});

test("a bucket nobody covered stays null so the line breaks", () => {
  const history = mixedFleet();
  const series = fleetPowerSeries(history);
  const rows = fleetPowerChartRows(history, series);
  assert.equal(rows.length, 3);
  assert.equal(rows[2]["system:measured"], null, "an unobserved bucket is not zero watts");
  assert.equal(rows[2]["system:estimated"], null);
});

test("the latest reading skips trailing empty buckets rather than reading zero", () => {
  const history = mixedFleet();
  const latest = latestReading(history, "system", "measured");
  assert.equal(latest.watts, 142);
  assert.equal(latest.machines, 3);
  assert.equal(latest.timestamp, T0 + 2 * 60_000);
});

test("a genuine zero-watt reading survives", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 1,
          measured: kind({ machines: 1 }),
          buckets: [bucket(0, { measured_watts: 0, measured_machines: 1 })],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  assert.equal(latestReading(history, "system", "measured").watts, 0);
  assert.equal(formatWatts(0), "0.00 W", "zero prints as a reading, not as an em dash");
});

/* --- coverage --- */

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
  const rows = fleetPowerChartRows(history, fleetPowerSeries(history));
  assert.equal(rows[0].gap, false);
  assert.equal(rows[1].gap, true);
  assert.equal(rows[1]["system:measured"], 100, "a gap flag does not blank the reading beside it");
});

/* --- cost --- */

test("cost keeps measured and estimated apart, and needs a stated rate", () => {
  const history = mixedFleet();
  const none = fleetPowerCost(history, "system", { currency: "EUR", per_kwh: null }, "1h");
  assert.equal(none.measured.cost, null, "no rate, no cost — there is no default tariff");
  assert.equal(none.estimated.cost, null);
  assert.equal(none.measured.energyKWh, 0.71, "the energy is known even when the price is not");

  const priced = fleetPowerCost(history, "system", { currency: "EUR", per_kwh: 0.28 }, "1h");
  assert.ok(Math.abs(priced.measured.cost - 0.71 * 0.28) < 1e-12);
  assert.ok(Math.abs(priced.estimated.cost - 0.09 * 0.28) < 1e-12);
  assert.equal(priced.currency, "EUR");
  assert.notEqual(priced.measured.cost + priced.estimated.cost, priced.measured.cost, "two costs, reported as two");
});

test("a free-power rate of zero is a price, and costs zero", () => {
  const priced = fleetPowerCost(mixedFleet(), "system", { currency: "USD", per_kwh: 0 }, "1h");
  assert.equal(priced.measured.cost, 0);
  assert.notEqual(priced.measured.cost, null, "zero is a stated rate, not an absent one");
});

test("the observed fraction says how much of the window is real", () => {
  const history = mixedFleet();
  const cost = fleetPowerCost(history, "system", { currency: "USD", per_kwh: 0.1 }, "1h");
  // 3 machines × 1 hour = 10800 machine-seconds possible; 10800 observed.
  assert.equal(cost.measured.observedFraction, 1);
  // 1 machine × 1 hour = 3600 possible; 3600 observed.
  assert.equal(cost.estimated.observedFraction, 1);

  const partial = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 2,
          measured: kind({ machines: 2, energy_kwh: 0.1, observed_machine_seconds: 1800 }),
          buckets: [bucket(0, { measured_watts: 100, measured_machines: 2 })],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  // 2 machines × 1 hour = 7200 possible; 1800 observed = a quarter.
  assert.equal(fleetPowerCost(partial, "system", { currency: "USD", per_kwh: 1 }, "1h").measured.observedFraction, 0.25);
  assert.equal(fleetPowerCost(partial, "system", null, "1h").measured.observedFraction, 0.25);
  assert.equal(fleetPowerCost(partial, "system", null, "nonsense").measured.observedFraction, null);
});

/* --- input handling --- */

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

test("buckets are sorted by time regardless of arrival order", () => {
  const history = normalizeFleetPower(
    response({
      domains: [
        domain("system", {
          reporting_machines: 1,
          measured: kind({ machines: 1 }),
          buckets: [
            bucket(2, { measured_watts: 30, measured_machines: 1 }),
            bucket(0, { measured_watts: 10, measured_machines: 1 }),
            bucket(1, { measured_watts: 20, measured_machines: 1 }),
          ],
        }),
        domain("cpu"),
        domain("dram"),
        domain("gpu"),
      ],
    }),
  );
  assert.deepEqual(
    domainOf(history, "system").buckets.map((b) => b.measured),
    [10, 20, 30],
  );
  assert.equal(latestReading(history, "system", "measured").watts, 30);
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

test("the pane keeps the collapsible-section contract it inherited", () => {
  // "capacity" is the persistence key in workspace-prefs. Renaming the section
  // would silently unfold this pane for every operator who had folded it.
  assert.match(PANE, /<Disclosure\s[\s\S]*?id="capacity"/);
  assert.match(PANE, /className="mf-section min-w-0"/);
  assert.match(PANE, /data-open=\{open \? "true" : "false"\}/);
  // The pane is one of the equal-width panes, not a full-width block.
  assert.match(PAGE, /<div className="mf-pane-grid mt-5">[\s\S]*?<FleetPowerPane/);
  // The pane it replaced is gone rather than merely unimported.
  assert.doesNotMatch(PAGE, /CapacityPane/);
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
