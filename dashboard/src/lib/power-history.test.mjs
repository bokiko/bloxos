import test from "node:test";
import assert from "node:assert/strict";
import { mergePowerHistory, powerChartPoints, powerProblemLabel, powerSensorIDs, sampleAgeLabel } from "./power-history.mjs";

const stats = (mean = 100, peak = 200, samples = 30) => ({ mean_watts: mean, peak_watts: peak, samples });

test("rejected telemetry has a useful bounded diagnostic, not raw server errors", () => {
  assert.match(powerProblemLabel("clock_skew"), /Correct its clock/);
  assert.match(powerProblemLabel("storage_error"), /will retry/);
  assert.equal(powerProblemLabel("raw sensitive error"), null);
  assert.equal(powerProblemLabel("__proto__"), null);
});
const bucket = (seq, extra = {}) => ({ stream_id: "a", seq, start_unix_ms: seq * 30000,
  end_unix_ms: (seq + 1) * 30000, expected_samples: 30,
  gpus: [{ id: "GPU-a", ...stats() }, { id: "GPU-b", ...stats() }], ...extra });

test("never fabricate GPU totals by summing independent peaks", () => {
  assert.equal(powerChartPoints([bucket(1)], "gpu_total")[0].peak, null);
  assert.equal(powerChartPoints([bucket(1, { gpu_total: stats(180, 250) })], "gpu_total")[0].peak, 250);
});
test("unknown is not zero and valid zero remains visible", () => {
  const points = [bucket(1, { cpu: stats(null, null, 0) }), bucket(2, { cpu: stats(0, 0) })];
  assert.deepEqual(powerChartPoints(points, "cpu").map((p) => p.mean), [null, 0]);
});
test("gaps break lines for missing sequences, restarts and declared loss", () => {
  for (const next of [bucket(3), bucket(2, { stream_id: "b" }), bucket(2, { gap_before: true })]) {
    const chart = powerChartPoints([next, bucket(1)], "GPU-a");
    assert.equal(chart.length, 3);
    assert.equal(chart[1].mean, null);
  }
});
test("partial sampling reports coverage, not invented full-window data", () => {
  assert.equal(powerChartPoints([bucket(1, { cpu: stats(100, 150, 15) })], "cpu")[0].coverage, 50);
});
test("invalid values are unavailable; sensor IDs are stable and unique", () => {
  assert.equal(powerChartPoints([bucket(1, { cpu: stats(Infinity, 2) })], "cpu")[0].mean, null);
  assert.deepEqual(powerSensorIDs([bucket(1), bucket(2)]), ["GPU-a", "GPU-b"]);
});
test("sample age exposes stale and clock-skewed data", () => {
  assert.equal(sampleAgeLabel(10000, 45000), "35s ago");
  assert.equal(sampleAgeLabel(10000, 135000), "2m ago");
  assert.equal(sampleAgeLabel(50000, 10000), "Machine clock ahead");
  assert.equal(sampleAgeLabel(undefined, 10000), "No samples");
});

test("delta history merges late backfill, deduplicates and refreshes gap metadata", () => {
  const initial = { points: [bucket(5)], gaps: [], degraded: true, cursor: 1 };
  const delta = { points: [bucket(2), bucket(5)], gaps: [{ stream_id: "a", from: 3, through: 4 }], degraded: false, cursor: 3 };
  const result = mergePowerHistory(initial, delta, 200000);
  assert.deepEqual(result.points.map((point) => point.seq).sort(), [2, 5]);
  assert.deepEqual(result.gaps, delta.gaps);
  assert.equal(result.degraded, false);
  assert.equal(result.cursor, 3);
});

test("empty deltas still expire local history; cursor reset requires full reload", () => {
  const initial = { points: [bucket(1)], gaps: [], degraded: false, cursor: 3 };
  const delta = { points: [], gaps: [], degraded: false, cursor: 3 };
  assert.equal(mergePowerHistory(initial, delta, 86400000 + 60001).points.length, 0);
  assert.throws(() => mergePowerHistory(initial, { ...delta, cursor: 1 }, 0), RangeError);
  assert.throws(() => mergePowerHistory(undefined, { ...delta, cursor: NaN }, 0));
});
