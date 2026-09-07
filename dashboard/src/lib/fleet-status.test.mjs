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
