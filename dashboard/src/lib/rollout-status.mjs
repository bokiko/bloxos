/**
 * Rollout status, turned into what the Versions page should show.
 *
 * Pure and separately tested, because the render is where this went wrong:
 * the hub sends `agent_rollout.unavailable` as a STRING, the component cast
 * every value to an object, and a hub with no rollout controller displayed a
 * green "Automatic · 0 updated · 0 validated · 0 pending" — the healthiest
 * possible badge for a rollout that cannot run at all.
 */

export const ROLLOUT_UNAVAILABLE = "unavailable";
export const ROLLOUT_HALTED = "halted";
export const ROLLOUT_ACTIVE = "active";

/**
 * Normalise one entry of the agent_rollout map.
 *
 * Two shapes arrive here and they are not interchangeable:
 *   "unavailable": "<reason>"            — a string, the whole controller
 *   "<platform>": { status, summary, … } — one platform
 * plus a per-platform read failure, which is an object whose status is
 * "unavailable" and which carries a reason rather than any counts.
 */
export function normalizeRolloutEntry(key, value) {
  if (typeof value === "string") {
    return { key, platform: key === ROLLOUT_UNAVAILABLE ? "Rollout" : key,
             state: ROLLOUT_UNAVAILABLE, reason: value };
  }
  if (!value || typeof value !== "object") {
    return { key, platform: key, state: ROLLOUT_UNAVAILABLE,
             reason: "the hub reported no usable rollout state" };
  }
  if (value.status === ROLLOUT_UNAVAILABLE) {
    return { key, platform: value.platform ?? key, state: ROLLOUT_UNAVAILABLE,
             reason: value.reason || "the hub could not read this platform's rollout state" };
  }
  const counts = {
    updated: value.updated ?? 0,
    validated: value.validated ?? 0,
    validating: value.validating ?? 0,
    pending: value.pending ?? 0,
    withheld: value.withheld ?? 0,
    failed: value.failed ?? 0,
  };
  return {
    key,
    platform: value.platform ?? key,
    state: value.status === ROLLOUT_HALTED ? ROLLOUT_HALTED : ROLLOUT_ACTIVE,
    reason: value.status === ROLLOUT_HALTED ? value.halt_reason || "" : "",
    summary: value.summary || "",
    counts,
    withheldReasons: value.withheld_reasons ?? {},
    failedReasons: value.failed_reasons ?? {},
  };
}

export function rolloutEntries(agentRollout) {
  if (!agentRollout || typeof agentRollout !== "object") return [];
  return Object.entries(agentRollout).map(([key, value]) => normalizeRolloutEntry(key, value));
}

/**
 * The badge for one entry. "Automatic" describes the POLICY being enabled, not
 * that anything is happening — the counts and summary carry progress.
 */
export function rolloutBadge(entry) {
  switch (entry.state) {
    case ROLLOUT_UNAVAILABLE:
      return { tone: "critical", label: "Unavailable", detail: entry.reason };
    case ROLLOUT_HALTED:
      return { tone: "critical", label: "Halted", detail: entry.reason };
    default:
      return { tone: "ok", label: "Automatic", detail: entry.summary };
  }
}

/**
 * Counts, with their scope stated.
 *
 * "Updated" is about THIS rollout: machines it has seen running the candidate,
 * including any whose validation later failed — they are demonstrably on the
 * new build. It is not a fleet total, and machines that were already current
 * and so never needed a slot are not represented at all.
 */
export function rolloutCountsLabel(entry) {
  if (entry.state === ROLLOUT_UNAVAILABLE) return "";
  const { updated, validated, validating, pending, withheld, failed } = entry.counts;
  const parts = [
    `${updated} on this build`,
    `${validated} validated`,
  ];
  if (validating > 0) parts.push(`${validating} validating`);
  if (pending > 0) parts.push(`${pending} offered`);
  if (withheld > 0) parts.push(`${withheld} withheld`);
  if (failed > 0) parts.push(`${failed} failed`);
  return parts.join(" · ");
}

/** Per-machine reasons, so a withheld or failed machine is nameable. */
export function rolloutReasonLines(entry) {
  if (entry.state === ROLLOUT_UNAVAILABLE) return [];
  return [
    ...Object.entries(entry.withheldReasons ?? {}).map(([id, why]) => `${id} withheld: ${why}`),
    ...Object.entries(entry.failedReasons ?? {}).map(([id, why]) => `${id} failed: ${why}`),
  ];
}

/**
 * The FLEET-level control, which is the operator pause and nothing else.
 *
 * It used to be labelled "Rollout: Active/Paused", which was already loose and
 * became wrong when the automatic failure breaker was removed: the flag now
 * means only that a person has not pressed pause. A halted platform, or a hub
 * with no controller at all, would still have shown "Active". Health belongs
 * to the per-platform rows.
 */
export function operatorPauseLabel(paused) {
  return paused ? "On" : "Off";
}
