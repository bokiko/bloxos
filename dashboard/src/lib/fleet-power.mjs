// Reading GET /api/fleet/power/history honestly.
//
// The hub returns four domains side by side, each split into a MEASURED and an
// ESTIMATED series with its own coverage. Nothing here ever adds those two
// together, and nothing here ever adds one domain to another: the protocol is
// explicit that system, cpu, dram and gpu are disjoint scopes, and an estimate
// is a model, not a reading.
//
// The one judgement call this module makes is WHICH domain the pane leads
// with, and it is made from the data rather than from a hardcoded preference:
//
//   "system"    at least one machine has a whole-platform figure. This is wall
//               power — the number that is money — and it is charted as
//               measured vs estimated.
//   "component" no machine has a whole-platform counter, but CPU or GPU power
//               is being reported. Those are charted as two separate lines and
//               labelled component power, never summed into a wall-power claim.
//   "none"      nothing at all. The pane says so.
//
// Every reading may be null. Null is unavailable; zero is a real reading.

import { POWER_PERIODS } from "./workspace-prefs.mjs";

export { POWER_PERIODS };

import { freshnessOf, aggregateFreshness } from "./power-freshness.mjs";

/** Period → what the pane calls the window in prose. */
export const PERIOD_LABELS = {
  "30m": "last 30 minutes",
  "1h": "last hour",
  "6h": "last 6 hours",
  "24h": "last 24 hours",
};

/** Period → hours, for "per hour" arithmetic the reader can check. */
export const PERIOD_HOURS = { "30m": 0.5, "1h": 1, "6h": 6, "24h": 24 };

const DOMAIN_LABELS = {
  system: "whole-machine",
  cpu: "CPU package",
  dram: "memory",
  gpu: "GPU",
};

export function domainLabel(domain) {
  return Object.hasOwn(DOMAIN_LABELS, domain) ? DOMAIN_LABELS[domain] : domain;
}

function isFiniteNumber(value) {
  return typeof value === "number" && Number.isFinite(value);
}

/** A watt reading, or null. Zero survives; anything unusable becomes null. */
function watts(value) {
  return isFiniteNumber(value) && value >= 0 ? value : null;
}

function count(value) {
  return Number.isInteger(value) && value >= 0 ? value : 0;
}

function kwh(value) {
  return isFiniteNumber(value) && value >= 0 ? value : 0;
}

function sourceList(value) {
  return Array.isArray(value) ? value.filter((s) => typeof s === "string" && s !== "") : [];
}

/**
 * Coerce the hub response into a shape the pane can render without guarding
 * every field. A response that is not recognisably fleet power throws, because
 * rendering a partially-understood power total is worse than rendering none.
 */
export function normalizeFleetPower(raw) {
  if (!raw || typeof raw !== "object" || !Array.isArray(raw.domains) || !raw.coverage) {
    throw new Error("Invalid fleet power response.");
  }
  const domains = new Map();
  for (const d of raw.domains) {
    if (!d || typeof d.domain !== "string" || !Array.isArray(d.buckets)) continue;
    domains.set(d.domain, {
      domain: d.domain,
      complete: d.complete === true,
      reportingMachines: count(d.reporting_machines),
      measured: normalizeKind(d.measured),
      estimated: normalizeKind(d.estimated),
      // Readings whose backend the hub could not classify. Never folded into
      // measured or modelled; surfaced so the omission is visible.
      unknown: normalizeKind(d.unknown),
      buckets: d.buckets
        .filter((b) => b && isFiniteNumber(b.end_unix_ms))
        .map((b) => ({
          timestamp: b.end_unix_ms,
          measured: watts(b.measured_watts),
          measuredMachines: count(b.measured_machines),
          estimated: watts(b.estimated_watts),
          estimatedMachines: count(b.estimated_machines),
          measuredWindowSeconds: kwh(b.measured_window_seconds),
          estimatedWindowSeconds: kwh(b.estimated_window_seconds),
          unknownMachines: count(b.unknown_machines),
          gap: b.gap === true,
        }))
        .sort((a, b) => a.timestamp - b.timestamp),
    });
  }
  const c = raw.coverage;
  return {
    period: POWER_PERIODS.includes(raw.period) ? raw.period : "6h",
    bucketSeconds: count(raw.bucket_seconds),
    startUnixMS: isFiniteNumber(raw.start_unix_ms) ? raw.start_unix_ms : null,
    endUnixMS: isFiniteNumber(raw.end_unix_ms) ? raw.end_unix_ms : null,
    domains,
    coverage: {
      machinesTotal: count(c.machines_total),
      machinesReporting: count(c.machines_reporting),
      machinesSilent: count(c.machines_silent),
      silentMachineIDs: sourceList(c.silent_machine_ids),
      machinesAPIPolled: count(c.machines_api_polled),
      degradedMachines: count(c.degraded_machines),
      gapsDeclared: count(c.gaps_declared),
      gapBuckets: count(c.gap_buckets),
      machinesSkewed: count(c.machines_skewed),
      truncated: c.truncated === true,
    },
  };
}

function normalizeKind(raw) {
  const k = raw && typeof raw === "object" ? raw : {};
  return {
    machines: count(k.machines),
    energyKWh: kwh(k.energy_kwh),
    // Retained under its old name for compatibility, but it is a SPAN, not
    // time observed. Nothing may present it as "% of the window measured".
    observedMachineSeconds: kwh(k.observed_machine_seconds),
    reportingWindowSeconds: kwh(k.reporting_window_seconds),
    // How much of those windows was actually read. The only defensible
    // statement about coverage — and never to be multiplied into a duration,
    // because backends sample at different cadences by design.
    sampleCount: count(k.sample_count),
    expectedSampleCount: count(k.expected_sample_count),
    // The newest AGENT window end behind this figure. Freshness is judged from
    // this, never from a chart bucket edge.
    latestObservationEndMS: isFiniteNumber(k.latest_observation_end_unix_ms)
      ? k.latest_observation_end_unix_ms
      : null,
    sources: sourceList(k.sources),
  };
}

const EMPTY_KIND = {
  machines: 0,
  energyKWh: 0,
  observedMachineSeconds: 0,
  reportingWindowSeconds: 0,
  sampleCount: 0,
  expectedSampleCount: 0,
  latestObservationEndMS: null,
  sources: [],
};

const EMPTY_DOMAIN = {
  domain: "",
  complete: false,
  reportingMachines: 0,
  measured: EMPTY_KIND,
  estimated: EMPTY_KIND,
  unknown: EMPTY_KIND,
  buckets: [],
};

export function domainOf(history, domain) {
  return history?.domains?.get(domain) ?? { ...EMPTY_DOMAIN, domain };
}

function domainHasData(history, domain) {
  return domainOf(history, domain).reportingMachines > 0;
}

/**
 * Normalise GET /api/fleet/power/current — the fleet's draw right now.
 *
 * This is a SEPARATE request from the history on purpose. The current figure
 * used to be read off the charted buckets, which made it depend on the selected
 * period and let it reach hours backwards for "the last non-null value". It is
 * now computed by the hub from each machine's newest window over a fixed
 * lookback, with every contributor gated individually.
 *
 * A caller that cannot get this must render UNAVAILABLE. Falling back to the
 * history aggregate and labelling it current would restore the exact defect
 * this replaced.
 */
export function normalizeFleetPowerCurrent(raw) {
  if (!raw || typeof raw !== "object" || !Array.isArray(raw.domains)) {
    throw new Error("Invalid fleet power snapshot.");
  }
  const domains = new Map();
  for (const d of raw.domains) {
    if (!d || typeof d.domain !== "string") continue;
    domains.set(d.domain, {
      domain: d.domain,
      measured: normalizeCurrentSeries(d.measured),
      estimated: normalizeCurrentSeries(d.estimated),
      unknownMachines: count(d.unknown_machines),
      staleMachines: count(d.stale_machines),
      skewedMachines: count(d.skewed_machines),
      unreadableMachines: count(d.unreadable_machines),
    });
  }
  return {
    generatedUnixMS: isFiniteNumber(raw.generated_unix_ms) ? raw.generated_unix_ms : null,
    lookbackMS: count(raw.lookback_ms),
    domains,
    machinesTotal: count(raw.machines_total),
    machinesReporting: count(raw.machines_reporting),
    machinesUnreadable: count(raw.machines_unreadable),
  };
}

function normalizeCurrentSeries(raw) {
  const k = raw && typeof raw === "object" ? raw : {};
  return {
    // null is unavailable. Zero would be a claim that the fleet draws nothing.
    watts: watts(k.watts),
    machines: count(k.machines),
    sources: sourceList(k.sources),
    // The sum is only as current as its OLDEST contributor, so that is what
    // freshness must be judged from.
    oldestContributorEndMS: isFiniteNumber(k.oldest_contributor_end_unix_ms)
      ? k.oldest_contributor_end_unix_ms
      : null,
    newestContributorEndMS: isFiniteNumber(k.newest_contributor_end_unix_ms)
      ? k.newest_contributor_end_unix_ms
      : null,
  };
}

const EMPTY_CURRENT_SERIES = {
  watts: null,
  machines: 0,
  sources: [],
  oldestContributorEndMS: null,
  newestContributorEndMS: null,
};

const EMPTY_CURRENT_DOMAIN = {
  domain: "",
  measured: EMPTY_CURRENT_SERIES,
  estimated: EMPTY_CURRENT_SERIES,
  unknownMachines: 0,
  staleMachines: 0,
  skewedMachines: 0,
  unreadableMachines: 0,
};

export function currentDomainOf(snapshot, domain) {
  return snapshot?.domains?.get(domain) ?? { ...EMPTY_CURRENT_DOMAIN, domain };
}

/**
 * The current reading for one domain and kind, with freshness attached.
 *
 * Freshness comes from the OLDEST contributor, so one still-reporting machine
 * cannot make a mostly-dark fleet read as current. Returns null when there is
 * nothing to show — and null means unavailable, never zero.
 */
export function currentReading(snapshot, domain, kind, nowMS = Date.now()) {
  const d = currentDomainOf(snapshot, domain);
  // Only the two kinds that carry a series exist here. An unrecognised kind is
  // REJECTED rather than falling through to the modelled series: quietly
  // treating "unknown" as "estimated" would put an unclassified reading behind
  // a label that claims to know what produced it.
  let series;
  if (kind === "measured") series = d.measured;
  else if (kind === "estimated") series = d.estimated;
  else return null;

  // watts === null is unavailable. A real measured 0 W is a reading and must
  // survive: a machine can genuinely draw nothing measurable on a domain.
  if (series.watts === null || series.machines === 0) return null;
  return {
    watts: series.watts,
    machines: series.machines,
    sources: series.sources,
    freshness: aggregateFreshness(
      series.oldestContributorEndMS === null ? [] : [series.oldestContributorEndMS],
      nowMS,
    ),
  };
}

/** Every domain the response can chart, in a fixed, meaningful order. */
export const POWER_DOMAINS = ["system", "cpu", "gpu", "dram"];

/**
 * Which domains actually have contributors, in POWER_DOMAINS order.
 *
 * This replaces the old automatic "mode": system power, when any machine had
 * it, silently took over the whole chart and hid every other domain. That was
 * fine while only measured system counters existed — and became actively
 * harmful the moment a MODELLED system reading could appear, because one
 * estimating board arriving in the fleet would evict the measured CPU and GPU
 * history of every other machine. A single old system bucket anywhere in the
 * selected period was enough to hold that choice for the entire window.
 *
 * Domain choice is now the reader's, and it is explicit.
 */
export function availableDomains(history) {
  return POWER_DOMAINS.filter((d) => domainHasData(history, d));
}

/**
 * Default domain order when the reader has expressed no preference.
 *
 * Components first: CPU and GPU are what most fleets actually measure, and a
 * whole-system figure is comparatively rare and — on a board with no counter —
 * may be modelled. Leading with system would hand the default view to an
 * estimate while real measurements sat behind it.
 */
export const DOMAIN_DEFAULT_ORDER = ["cpu", "gpu", "system", "dram"];

/**
 * The lines to chart for ONE selected domain: measured and modelled shown
 * independently, never summed and never merged.
 *
 * There is deliberately no "pick the interesting domain for me" here. That
 * behaviour used to live in a fleetPowerMode()/fleetPowerSeries() pair which
 * let whole-machine power take over the chart whenever any machine reported it,
 * hiding every other domain — and would have let one estimating board evict the
 * measured history of the whole fleet. Domain choice belongs to the reader.
 */
export function domainSeries(history, domain) {
  const d = domainOf(history, domain);
  const series = [];
  if (d.measured.machines > 0) {
    series.push({ key: `${domain}:measured`, domain, kind: "measured", label: "Measured" });
  }
  if (d.estimated.machines > 0) {
    series.push({ key: `${domain}:estimated`, domain, kind: "estimated", label: "Modelled" });
  }
  return series;
}

/**
 * The series to render for a domain, from BOTH the history and the current
 * snapshot.
 *
 * Deriving them from history alone loses live data. The history request is
 * capped (the oldest rows are dropped first) and a machine that only just
 * started reporting has almost none — so a fleet could be drawing 100 W on CPU
 * right now, offer a CPU button, and render no readout at all behind it.
 *
 * A kind appears if EITHER source has it. The chart simply draws null rows for
 * a kind with no history yet, which is the honest picture: a current value and
 * no past to plot.
 */
export function combinedSeries(history, snapshot, domain) {
  const h = domainOf(history, domain);
  const c = currentDomainOf(snapshot, domain);
  const series = [];
  if (h.measured.machines > 0 || c.measured.machines > 0) {
    series.push({ key: `${domain}:measured`, domain, kind: "measured", label: "Measured" });
  }
  if (h.estimated.machines > 0 || c.estimated.machines > 0) {
    series.push({ key: `${domain}:estimated`, domain, kind: "estimated", label: "Modelled" });
  }
  return series;
}

/**
 * Resolve which domain to show.
 *
 * An EXPLICIT stored choice always wins, even when that domain currently has no
 * data. A reader who selected DRAM is asking to watch DRAM; silently moving
 * them elsewhere the moment it goes quiet would make the view jump around
 * under them and hide the very fact they selected it to see. An empty selected
 * domain renders as empty, which is information.
 *
 * With no stored choice, the default is deterministic: the first domain in
 * DOMAIN_DEFAULT_ORDER with a MEASURED contributor, then the first with any
 * contributor at all (which may be modelled), then "cpu" so the shape is stable
 * when nothing has reported. Callers must LATCH this once rather than
 * recomputing it on every poll, or an arriving domain could move the view.
 */
export function resolveDomainChoice(history, stored) {
  if (stored && POWER_DOMAINS.includes(stored)) return stored;
  const measuredFirst = DOMAIN_DEFAULT_ORDER.find(
    (d) => domainOf(history, d).measured.machines > 0,
  );
  if (measuredFirst) return measuredFirst;
  const anyFirst = DOMAIN_DEFAULT_ORDER.find((d) => domainHasData(history, d));
  return anyFirst ?? "cpu";
}

export function fleetPowerChartRows(history, series) {
  const rows = new Map();
  for (const s of series) {
    for (const bucket of domainOf(history, s.domain).buckets) {
      let row = rows.get(bucket.timestamp);
      if (!row) {
        row = { timestamp: bucket.timestamp, gap: false };
        rows.set(bucket.timestamp, row);
      }
      row[s.key] = s.kind === "measured" ? bucket.measured : bucket.estimated;
      if (bucket.gap) row.gap = true;
    }
  }
  return [...rows.values()].sort((a, b) => a.timestamp - b.timestamp);
}

/**
 * The most recent bucket that actually carried a reading for a series, with
 * the machine count behind it. Trailing empty buckets are skipped rather than
 * reported as a drop to nothing: the window's leading edge is usually empty
 * simply because the current bucket has not closed.
 */
/**
 * WITHHELD IN THIS RELEASE. Energy and cost are not presented at all.
 *
 * They used to be shown as "At least X kWh / $Y", i.e. a guaranteed lower
 * bound. They are not one, and the shortfall is not a rounding matter:
 *
 *   - A window's mean is the mean of the reads that SUCCEEDED inside it, and
 *     the hub then weights that mean by the window's WHOLE span. One successful
 *     300 W read in a 30 s window contributes 9000 W·s as though all thirty
 *     seconds had been observed. The unread seconds could have drawn far less,
 *     so the figure is neither an upper nor a lower bound.
 *   - `observedFraction` compounded it by dividing summed window spans by the
 *     period, presenting "5.2 of 6 hours observed" as measured coverage. Spans
 *     are not deduplicated — ingest does not enforce non-overlapping windows
 *     across streams — and a window binned by its end can carry time belonging
 *     to the previous bucket. The ratio was not a fraction of time observed.
 *
 * An honest energy figure needs each observation to carry its own measured
 * duration, which is additive protocol work and deliberately not in this
 * change. Until then the readout is omitted rather than qualified: a number
 * this wrong cannot be rescued by a caption, and the compact pane has nowhere
 * to put the qualification it would need.
 *
 * The function is retained so callers keep compiling, and reports
 * `available: false` with the reason. Consumers must render the reason, not a
 * zero — "no energy accounting" is not "0 kWh".
 */
export function fleetPowerEnergyAvailability() {
  return {
    available: false,
    reason:
      "Energy and cost accounting is unavailable: sampled power cannot be " +
      "extrapolated into measured energy without per-observation durations.",
  };
}

/**
 * "3 of 6 machines reporting" — the sentence that stops a partial sum being
 * read as a fleet total. It never says "all" unless every machine reported.
 */
export function coverageSentence(coverage) {
  const { machinesTotal: total, machinesReporting: reporting } = coverage;
  if (total === 0) return "No machines in this fleet.";
  if (reporting === 0) return `No power readings from any of ${total} machines.`;
  if (reporting === total) {
    return `All ${total} ${total === 1 ? "machine is" : "machines are"} reporting power.`;
  }
  return `${reporting} of ${total} machines reporting power.`;
}

/**
 * Why the other machines are missing, where that is actually known. An
 * API-polled machine has no agent and cannot report power, which is a
 * different fact from a machine that should be reporting and is not.
 */
export function shortfallSentence(coverage) {
  const missing = coverage.machinesTotal - coverage.machinesReporting;
  if (missing <= 0) return null;
  const apiPolled = Math.min(coverage.machinesAPIPolled, missing);
  const rest = missing - apiPolled;
  const parts = [];
  if (apiPolled > 0) {
    parts.push(`${apiPolled} API-polled (no agent, so no power counter)`);
  }
  if (rest > 0) {
    parts.push(`${rest} with no power counter reporting`);
  }
  return parts.length > 0 ? parts.join(" · ") : null;
}

/** Warnings that must be shown beside any number drawn from this window. */
export function fleetPowerWarnings(history) {
  const out = [];
  const c = history?.coverage;
  if (!c) return out;
  if (c.gapBuckets > 0 || c.gapsDeclared > 0) {
    out.push("History has gaps. Missing data is not zero power.");
  }
  if (c.degradedMachines > 0) {
    out.push(
      `${c.degradedMachines} ${c.degradedMachines === 1 ? "machine is" : "machines are"} recording power with reduced coverage.`,
    );
  }
  if (c.truncated) {
    out.push("Too much history to read at once — the oldest part of this window is missing.");
  }
  return out;
}

/** Watts, at a precision the sensor can support. Null prints as an em dash. */
export function formatWatts(value) {
  if (value === null || value === undefined) return "—";
  if (!isFiniteNumber(value)) return "—";
  if (value >= 100) return `${Math.round(value)} W`;
  if (value >= 10) return `${value.toFixed(1)} W`;
  return `${value.toFixed(2)} W`;
}

/** Money at the reader's locale. Falls back to a bare number on a bad code. */
export function formatMoney(amount, currency) {
  if (!isFiniteNumber(amount)) return "—";
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency,
      // Electricity over half an hour is fractions of a unit; two decimals
      // would round a real cost to a confident zero.
      minimumFractionDigits: 2,
      maximumFractionDigits: amount < 1 ? 4 : 2,
    }).format(amount);
  } catch {
    return `${amount.toFixed(4)} ${currency}`;
  }
}

export function formatKWh(value) {
  if (!isFiniteNumber(value)) return "—";
  if (value >= 10) return `${value.toFixed(1)} kWh`;
  if (value >= 1) return `${value.toFixed(2)} kWh`;
  return `${value.toFixed(3)} kWh`;
}
