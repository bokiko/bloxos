import test from "node:test";
import assert from "node:assert/strict";
import { detailStatus, terminalStartError } from "./machine-status.mjs";

const NOW = 1_800_000_000_000;

test("detail page freshness matches the fleet policy over time", () => {
  // Fresh heartbeat: live.
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 5_000, now: NOW }), "live");
  // Warning thresholds still win while the data is fresh.
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 5_000, now: NOW, maxGpuTempC: 81 }), "warning");
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 5_000, now: NOW, diskPct: 91 }), "warning");
  // Same inputs, time passing with no new metrics: live -> stale -> offline.
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 31_000, now: NOW }), "stale");
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 121_000, now: NOW }), "offline");
  // A stale REST snapshot from page open must not keep a quiet agent "live":
  // apiStatus stays "online" but the heartbeat aged out.
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 200_000, now: NOW }), "offline");
});

test("hub-known offline always wins; missing heartbeat is offline", () => {
  assert.equal(detailStatus({ apiStatus: "offline", lastSeenMs: NOW - 1_000, now: NOW }), "offline");
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: 0, now: NOW }), "offline");
});

test("hot readings from stale data report stale, keeping controls disabled", () => {
  // 31-120s-old heartbeat with a hot GPU must be stale (controls disabled),
  // not warning (controls enabled).
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 40_000, now: NOW, maxGpuTempC: 95 }), "stale");
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 40_000, now: NOW, diskPct: 95 }), "stale");
  // Fresh + hot stays warning (controls enabled — data is current).
  assert.equal(detailStatus({ apiStatus: "online", lastSeenMs: NOW - 5_000, now: NOW, maxGpuTempC: 95 }), "warning");
});

test("terminal start errors are actionable", () => {
  assert.match(terminalStartError(429, ""), /Too many terminal attempts/);
  assert.match(terminalStartError(404, ""), /agent is not connected/);
  assert.match(terminalStartError(503, ""), /connection is unavailable/);
  assert.match(terminalStartError(500, "db exploded"), /db exploded/);
  assert.match(terminalStartError(500, ""), /HTTP 500/);
});
