// Keep unavailable readings null. Never reinterpret legacy instantaneous watts
// as a 30-second mean, or sum independent GPU peaks into a machine peak.
export function powerStats(point, sensor) {
  if (sensor === "gpu_total") return point.gpu_total;
  if (sensor === "cpu") return point.cpu;
  return point.gpus?.find((gpu) => gpu.id === sensor);
}

// A window is readable only when the agent actually sampled it and the numbers
// are self-consistent. A peak below its own mean is a corrupt record, not a low
// reading, so it is dropped rather than drawn or averaged.
export function validPowerStats(stats) {
  return Boolean(stats) && Number.isInteger(stats.samples) && stats.samples > 0 &&
    Number.isFinite(stats.mean_watts) && stats.mean_watts >= 0 &&
    Number.isFinite(stats.peak_watts) && stats.peak_watts >= stats.mean_watts;
}

export function powerChartPoints(points, sensor) {
  const ordered = points.filter((point) =>
    Number.isFinite(point.start_unix_ms) && Number.isFinite(point.end_unix_ms) &&
    point.end_unix_ms > point.start_unix_ms
  ).sort((a, b) => a.start_unix_ms - b.start_unix_ms || a.seq - b.seq);
  const result = [];
  let previous;
  for (const point of ordered) {
    if (previous && (point.gap_before || point.stream_id !== previous.stream_id ||
      point.seq !== previous.seq + 1 || point.start_unix_ms - previous.end_unix_ms > 1500)) {
      result.push({ timestamp: point.start_unix_ms, mean: null, peak: null, coverage: null });
    }
    const stats = powerStats(point, sensor);
    const valid = validPowerStats(stats);
    result.push({
      timestamp: point.end_unix_ms,
      mean: valid ? stats.mean_watts : null,
      peak: valid ? stats.peak_watts : null,
      coverage: valid && point.expected_samples > 0
        ? Math.min(100, 100 * stats.samples / point.expected_samples) : null,
    });
    previous = point;
  }
  return result;
}

export function powerSensorIDs(points) {
  return [...new Set(points.flatMap((point) => (point.gpus ?? []).map((gpu) => gpu.id)))].sort();
}

export function powerProblemLabel(problem) {
  const labels = {
    clock_skew: "The machine clock is more than 15 minutes ahead of the hub. Correct its clock to resume power history.",
    storage_error: "The hub could not save power history. The agent will retry; live metrics are unaffected.",
    conflicting_replay: "A saved power-history record conflicts with the hub's copy. Existing readings are preserved; agent diagnostics need review.",
    rejected_data: "The hub rejected a power-history record. Live metrics are unaffected; agent diagnostics need review.",
  };
  return Object.hasOwn(labels, problem) ? labels[problem] : null;
}

// Ingestion cursors (not measurement timestamps) preserve offline backfill.
// The server returns current gap/degraded metadata even on an empty delta.
export function mergePowerHistory(previous, incoming, now) {
  if (!Number.isSafeInteger(incoming.cursor) || incoming.cursor < 0 ||
    !Array.isArray(incoming.points) || !Array.isArray(incoming.gaps)) {
    throw new Error("Invalid power history response.");
  }
  if (previous && incoming.cursor < previous.cursor) {
    throw new RangeError("Power history changed on the hub; reloading.");
  }
  const cutoff = now - 24 * 60 * 60 * 1000;
  const points = new Map();
  for (const point of [...(previous?.points ?? []), ...incoming.points]) {
    if (point.end_unix_ms >= cutoff) points.set(JSON.stringify([point.stream_id, point.seq]), point);
  }
  return { ...incoming, points: [...points.values()] };
}

// The numbers beside the chart, for one sensor, over the points already loaded
// (at most 24 h — see mergePowerHistory's cutoff).
//
// `average` is sample-weighted: a window the agent only half-sampled carries
// half the weight of a full one, so the figure is the mean of every sample
// taken rather than the mean of the window means. `peak` is the highest single
// sampled peak of any window — never a sum, because independent sensors peak at
// different instants and adding them would invent a machine peak that never
// happened. `windows`/`samples` are what the average is made of, so the label
// beside it can say so instead of implying a full day.
export function powerRailStats(points, sensor) {
  let windows = 0;
  let samples = 0;
  let weighted = 0;
  let peak = null;
  for (const point of points) {
    const stats = powerStats(point, sensor);
    if (!validPowerStats(stats)) continue;
    windows += 1;
    samples += stats.samples;
    weighted += stats.mean_watts * stats.samples;
    peak = peak === null ? stats.peak_watts : Math.max(peak, stats.peak_watts);
  }
  return { windows, samples, average: samples > 0 ? weighted / samples : null, peak };
}

export function sampleAgeLabel(endUnixMS, now) {
  if (!Number.isFinite(endUnixMS)) return "No samples";
  if (endUnixMS > now + 5000) return "Machine clock ahead";
  const seconds = Math.max(0, Math.floor((now - endUnixMS) / 1000));
  return seconds < 60 ? `${seconds}s ago` : `${Math.floor(seconds / 60)}m ago`;
}
