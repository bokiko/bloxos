import assert from "node:assert/strict";
import test from "node:test";
import { computeFleetMetrics, isFreshMetrics } from "./fleet-metrics.mjs";
import { gaugeReading, latestGPUs } from "./gauge-data.mjs";

const cpuOnly = { cpu_percent: 0, gpus: [], gpu_util_percent: 0 };
const gpuMachine = {
  cpu_percent: 20,
  gpus: [{ util_percent: 100, temp_c: 105, mem_total_bytes: 100, mem_used_bytes: 80, power_watts: 250 }],
};

test("CPU-only machines do not invent GPUs or dilute GPU averages", () => {
  assert.equal(computeFleetMetrics([cpuOnly]).hasGpu, false);
  const fleet = computeFleetMetrics([cpuOnly, gpuMachine]);
  assert.equal(fleet.avgGpuUtil, 100);
  assert.equal(fleet.avgCpu, 10); // Idle CPUs still count.
  assert.equal(fleet.avgVramPct, 80);
  assert.equal(fleet.maxGpuTemp, 105);
});

test("legacy GPU capability includes idle GPUs, but not absent telemetry", () => {
  const legacy = { gpu_util_percent: 0, gpu_vram_total_bytes: 100 };
  const fleet = computeFleetMetrics([legacy, gpuMachine, {}]);
  assert.equal(fleet.hasGpu, true);
  assert.equal(fleet.avgGpuUtil, 50);
  assert.equal(fleet.avgCpu, 20);
});

test("each GPU contributes to the average and power total", () => {
  const fleet = computeFleetMetrics([{ gpus: [
    ...gpuMachine.gpus,
    { util_percent: 0, temp_c: 40, mem_total_bytes: 100, mem_used_bytes: 20, power_watts: 20 },
  ] }]);
  assert.equal(fleet.avgGpuUtil, 50);
  assert.equal(fleet.avgVramPct, 50);
  assert.equal(fleet.totalPower, 270);
});

test("freshness ages out without requiring another snapshot, regardless of load", () => {
  const now = 1_000_000;
  const machines = [
    { ...cpuOnly, last_seen: now },
    { ...gpuMachine, cpu_percent: 100, last_seen: now - 30_001 },
  ];
  const fresh = machines.filter((m) => isFreshMetrics(m.last_seen, now));
  assert.equal(fresh.length, 1);
  assert.equal(computeFleetMetrics(fresh).hasGpu, false);
  assert.equal(isFreshMetrics(now, now + 30_000), true);
  assert.equal(isFreshMetrics(now, now + 30_001), false);
  for (const lastSeen of [undefined, NaN, 0, now - 120_001]) {
    assert.equal(isFreshMetrics(lastSeen, now), false);
  }
});

test("live GPU snapshots replace initial data, including GPU removal", () => {
  const initial = [{ util_percent: 10 }];
  const live = [{ util_percent: 90 }];
  assert.equal(latestGPUs(live, initial), live);
  assert.deepEqual(latestGPUs([], initial), []);
  assert.equal(latestGPUs(undefined, initial), initial);
});

test("gauge labels preserve actual temperatures; only the arc is clamped", () => {
  assert.deepEqual(gaugeReading(105), { arc: 100, label: "105" });
  assert.deepEqual(gaugeReading(50), { arc: 50, label: "50" });
  assert.deepEqual(gaugeReading(-5), { arc: 0, label: "-5" });
  assert.deepEqual(gaugeReading(NaN), { arc: 0, label: "—" });
});
