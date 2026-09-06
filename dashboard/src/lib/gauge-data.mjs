/** @param {number} value */
export function gaugeReading(value) {
  // Only the arc is bounded. Temperature (and other absolute readings) must
  // retain the reported value, even beyond the chart's 0–100 scale.
  return Number.isFinite(value)
    ? { arc: Math.min(100, Math.max(0, value)), label: value.toFixed(0) }
    : { arc: 0, label: "—" };
}

/**
 * Empty live GPU arrays are authoritative; absent fields retain API fallback.
 * @param {import("./demo-data").GPUInfo[] | undefined} live
 * @param {import("./demo-data").GPUInfo[]} initial
 */
export function latestGPUs(live, initial) {
  return live ?? initial;
}
