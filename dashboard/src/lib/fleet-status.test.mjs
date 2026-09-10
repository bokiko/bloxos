import test from "node:test";
import assert from "node:assert/strict";
import { classifyMachine, METRICS_STALE_MS, OFFLINE_MS } from "./fleet-metrics.mjs";

const base = { last_seen: Date.now(), cpu_percent: 10, ram_used_bytes: 1, ram_total_bytes: 100, disk_used_bytes: 1, disk_total_bytes: 100, gpu_temp: 50 };

test("fresh hot machine still reports warning/critical", () => {
  assert.equal(classifyMachine({ ...base, cpu_percent: 95 }).status, "critical");
  assert.equal(classifyMachine({ ...base, gpu_temp: 80 }).status, "warning");
  assert.equal(classifyMachine(base).status, "live");
});

test("stale data is stale, never warning or critical", () => {
  const old = Date.now() - METRICS_STALE_MS - 1000;
  assert.equal(classifyMachine({ ...base, last_seen: old }).status, "stale");
  // Hot readings from stale data must not escalate (freshness outranks
  // thresholds; shared with the machine detail page's detailStatus).
  assert.equal(classifyMachine({ ...base, last_seen: old, cpu_percent: 99 }).status, "stale");
  assert.equal(classifyMachine({ ...base, last_seen: old, gpu_temp: 95 }).status, "stale");
});

test("no heartbeat past the cutoff is offline", () => {
  assert.equal(classifyMachine({ ...base, last_seen: Date.now() - OFFLINE_MS - 1000, cpu_percent: 99 }).status, "offline");
  assert.equal(classifyMachine({ ...base, last_seen: 0 }).status, "offline");
});

/* ============================================================================
 * Expected high load.
 *
 * A machine that mines, renders or trains is SUPPOSED to sit at 100% CPU, and
 * classifying it critical makes the one machine doing its job the loudest
 * alert on the dashboard. The flag suppresses exactly one signal — the CPU
 * rule — for exactly one machine, and says so in the result.
 * ========================================================================== */

const EXPECTED = { expectedHighCpu: true };

test("expected high load stops CPU saturation from escalating", () => {
  const pegged = { ...base, cpu_percent: 100 };
  assert.equal(classifyMachine(pegged).status, "critical");
  assert.equal(classifyMachine(pegged, EXPECTED).status, "live");
  // The warning band too, not only the critical one.
  assert.equal(classifyMachine({ ...base, cpu_percent: 80 }).status, "warning");
  assert.equal(classifyMachine({ ...base, cpu_percent: 80 }, EXPECTED).status, "live");
});

test("the suppression is reported, never silent", () => {
  const { status, suppressed } = classifyMachine({ ...base, cpu_percent: 100 }, EXPECTED);
  assert.equal(status, "live");
  assert.equal(suppressed, "CPU 100%");
});

test("an idle flagged machine reports no suppression", () => {
  // The flag is a standing policy; it only swallows a reading when there was
  // one to swallow, so an idle miner must not claim a suppressed CPU figure.
  assert.equal(classifyMachine(base, EXPECTED).suppressed, undefined);
  assert.equal(classifyMachine(base, EXPECTED).status, "live");
});

test("every other problem still escalates on a flagged machine", () => {
  const flagged = { ...base, cpu_percent: 100 };
  // Disk full.
  assert.equal(
    classifyMachine({ ...flagged, disk_used_bytes: 97, disk_total_bytes: 100 }, EXPECTED).status,
    "critical",
  );
  // Memory exhausted.
  assert.equal(
    classifyMachine({ ...flagged, ram_used_bytes: 97, ram_total_bytes: 100 }, EXPECTED).status,
    "critical",
  );
  // GPU overheating.
  assert.equal(classifyMachine({ ...flagged, gpu_temp: 90 }, EXPECTED).status, "critical");
  // Gone quiet.
  assert.equal(
    classifyMachine({ ...flagged, last_seen: Date.now() - METRICS_STALE_MS - 1000 }, EXPECTED).status,
    "stale",
  );
  assert.equal(
    classifyMachine({ ...flagged, last_seen: Date.now() - OFFLINE_MS - 1000 }, EXPECTED).status,
    "offline",
  );
});

test("a flagged machine escalating for another reason still names both", () => {
  // The disk is why it is critical; the CPU figure is why its state looks
  // calmer than the raw numbers suggest. The reader gets both.
  const out = classifyMachine(
    { ...base, cpu_percent: 100, disk_used_bytes: 97, disk_total_bytes: 100 },
    EXPECTED,
  );
  assert.equal(out.status, "critical");
  assert.equal(out.reason, "Disk 97%");
  assert.equal(out.suppressed, "CPU 100%");
});

test("the flag is per machine, never global", () => {
  // No options, or an explicit false, must behave exactly as before.
  assert.equal(classifyMachine({ ...base, cpu_percent: 100 }).status, "critical");
  assert.equal(
    classifyMachine({ ...base, cpu_percent: 100 }, { expectedHighCpu: false }).status,
    "critical",
  );
  assert.equal(classifyMachine({ ...base, cpu_percent: 100 }, {}).status, "critical");
});
