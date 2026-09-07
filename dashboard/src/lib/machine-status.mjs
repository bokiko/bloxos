// Machine detail status policy — the detail page must judge freshness exactly
// like the fleet (classifyMachine in StatusBadge.tsx): an explicit hub-known
// "offline" wins; otherwise age of the last heartbeat decides live → stale →
// offline. A REST snapshot fetched at page open must never keep calling an
// agent "live" after it stops reporting.
import { METRICS_STALE_MS, OFFLINE_MS } from "./fleet-metrics.mjs";

// detailStatus returns one of: "offline" | "warning" | "stale" | "live".
// Freshness outranks thresholds: a warning computed from data older than
// METRICS_STALE_MS is unknown, not current — so stale wins (same precedence
// as the fleet's classifyMachine).
export function detailStatus({ apiStatus, lastSeenMs, now, maxGpuTempC = 0, diskPct = 0 }) {
  if (apiStatus === "offline") return "offline";
  const age = now - lastSeenMs;
  if (!(lastSeenMs > 0) || age > OFFLINE_MS) return "offline";
  if (age > METRICS_STALE_MS) return "stale";
  if (maxGpuTempC > 80 || diskPct > 90) return "warning";
  return "live";
}

// terminalStartError turns a failed terminal-start HTTP response into an
// actionable message instead of a generic disconnect.
export function terminalStartError(status, hubError) {
  if (status === 429) return "Too many terminal attempts — wait a minute and retry.";
  if (status === 404) return "The agent is not connected; the machine must be online to open a terminal.";
  if (status === 503) return "The agent connection is unavailable right now; retry in a few seconds.";
  if (hubError) return `Terminal start failed: ${hubError}`;
  return `Terminal start failed (HTTP ${status}).`;
}
