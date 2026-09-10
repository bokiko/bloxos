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
      buckets: d.buckets
        .filter((b) => b && isFiniteNumber(b.end_unix_ms))
        .map((b) => ({
          timestamp: b.end_unix_ms,
          measured: watts(b.measured_watts),
          measuredMachines: count(b.measured_machines),
          estimated: watts(b.estimated_watts),
          estimatedMachines: count(b.estimated_machines),
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
      truncated: c.truncated === true,
    },
  };
}

function normalizeKind(raw) {
  const k = raw && typeof raw === "object" ? raw : {};
  return {
    machines: count(k.machines),
    energyKWh: kwh(k.energy_kwh),
    observedMachineSeconds: kwh(k.observed_machine_seconds),
    sources: sourceList(k.sources),
  };
}

const EMPTY_DOMAIN = {
  domain: "",
  complete: false,
  reportingMachines: 0,
  measured: { machines: 0, energyKWh: 0, observedMachineSeconds: 0, sources: [] },
  estimated: { machines: 0, energyKWh: 0, observedMachineSeconds: 0, sources: [] },
  buckets: [],
};

export function domainOf(history, domain) {
  return history?.domains?.get(domain) ?? { ...EMPTY_DOMAIN, domain };
}

function domainHasData(history, domain) {
  return domainOf(history, domain).reportingMachines > 0;
}

/**
 * Which story this response can honestly tell. See the module comment.
 */
export function fleetPowerMode(history) {
  if (!history) return "none";
  if (domainHasData(history, "system")) return "system";
  if (domainHasData(history, "cpu") || domainHasData(history, "gpu")) return "component";
  return "none";
}

/**
 * The lines to chart, as `{ key, label, kind, domain }`. `kind` is
 * "measured" or "estimated" and drives the stroke: a caller must render the
 * two differently AND print the label, never rely on colour.
 *
 * In component mode the two domains are separate lines. They are NOT summed:
 * CPU package watts plus GPU watts is not what the machine draws from the wall
 * — it omits everything else and double-counts nothing, so it is neither a
 * component figure nor a wall figure.
 */
export function fleetPowerSeries(history) {
  const mode = fleetPowerMode(history);
  if (mode === "system") {
    const system = domainOf(history, "system");
    const series = [];
    if (system.measured.machines > 0) {
      series.push({ key: "system:measured", domain: "system", kind: "measured", label: "Measured" });
    }
    if (system.estimated.machines > 0) {
      series.push({ key: "system:estimated", domain: "system", kind: "estimated", label: "Estimated" });
    }
    return series;
  }
  if (mode === "component") {
    const series = [];
    for (const domain of ["cpu", "gpu"]) {
      const d = domainOf(history, domain);
      if (d.measured.machines > 0) {
        series.push({ key: `${domain}:measured`, domain, kind: "measured", label: domainLabel(domain) });
      }
      if (d.estimated.machines > 0) {
        series.push({
          key: `${domain}:estimated`,
          domain,
          kind: "estimated",
          label: `${domainLabel(domain)} (estimated)`,
        });
      }
    }
    return series;
  }
  return [];
}

/**
 * Recharts rows: one per bucket, one column per series key. A bucket a series
 * did not cover stays null so the line breaks there instead of being drawn
 * across time nobody observed.
 */
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
export function latestReading(history, domain, kind) {
  const buckets = domainOf(history, domain).buckets;
  for (let i = buckets.length - 1; i >= 0; i -= 1) {
    const value = kind === "measured" ? buckets[i].measured : buckets[i].estimated;
    if (value === null) continue;
    return {
      watts: value,
      machines: kind === "measured" ? buckets[i].measuredMachines : buckets[i].estimatedMachines,
      timestamp: buckets[i].timestamp,
    };
  }
  return null;
}

/**
 * Cost for one domain at one tariff, measured and estimated kept apart.
 *
 * Both figures are FLOORS. The hub sums energy over observed windows only, so
 * time no machine reported contributes nothing — never an interpolation. The
 * `observedFraction` says how much of the window is real so the pane can say
 * "over 5.2 of 6 hours observed" instead of implying a full accounting.
 *
 * A null rate yields null costs. There is no default tariff.
 */
export function fleetPowerCost(history, domain, rate, period) {
  const d = domainOf(history, domain);
  const perKwh = rate && isFiniteNumber(rate.per_kwh) && rate.per_kwh >= 0 ? rate.per_kwh : null;
  const hours = Object.hasOwn(PERIOD_HOURS, period) ? PERIOD_HOURS[period] : null;

  const observedFraction = (kind) => {
    if (!hours || kind.machines === 0) return null;
    const possible = kind.machines * hours * 3600;
    return possible > 0 ? Math.min(1, kind.observedMachineSeconds / possible) : null;
  };

  return {
    currency: rate?.currency ?? "USD",
    perKwh,
    measured: {
      energyKWh: d.measured.energyKWh,
      cost: perKwh === null ? null : d.measured.energyKWh * perKwh,
      machines: d.measured.machines,
      observedFraction: observedFraction(d.measured),
    },
    estimated: {
      energyKWh: d.estimated.energyKWh,
      cost: perKwh === null ? null : d.estimated.energyKWh * perKwh,
      machines: d.estimated.machines,
      observedFraction: observedFraction(d.estimated),
    },
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
