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
  // Anything that is not a status this build understands is UNAVAILABLE, not
  // healthy. Defaulting the other way is the same defect in a second costume:
  // `{}` — a truncated response, a field an older hub never sent, a status
  // added by a newer one — would render a green "Automatic · 0 · 0 · 0",
  // which is indistinguishable from a rollout that has genuinely just begun.
  // An unrecognised status means the page does not know, and it must say so.
  if (value.status !== ROLLOUT_ACTIVE && value.status !== ROLLOUT_HALTED) {
    return {
      key,
      platform: value.platform ?? key,
      state: ROLLOUT_UNAVAILABLE,
      reason: value.status
        ? `the hub reported a rollout status this page does not understand: ${value.status}`
        : "the hub reported no rollout status for this platform",
    };
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
    // The build these counts are about. It is NOT necessarily what the hub
    // serves right now: a new candidate is only picked up when a reservation
    // runs, so while the operator pause is on the hub can serve one build and
    // the rollout still describe the previous one. Without this on screen,
    // "observed on this build" silently attaches old counts to a new binary.
    candidate: typeof value.candidate_sha === "string" ? value.candidate_sha : "",
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
 * "Updated" is HISTORICAL and scoped to this rollout: machines it has at some
 * point seen running the candidate, including any that later failed, rolled
 * back, or disconnected. It is not "currently on this build" and not a fleet
 * total — machines that were already current, and so never needed a slot, are
 * not represented at all. Hence "observed on this build", which is the claim
 * the underlying latch actually supports.
 */
export function rolloutCountsLabel(entry) {
  if (entry.state === ROLLOUT_UNAVAILABLE) return "";
  const { updated, validated, validating, pending, withheld, failed } = entry.counts;
  const parts = [
    `${updated} observed on this build`,
    `${validated} validated`,
  ];
  if (validating > 0) parts.push(`${validating} validating`);
  // "Pending attempts" — not "offered", and not "pending offers" either. A
  // slot is RESERVED before the write to the socket, so this includes
  // attempts never sent; and a resume can open a fresh validation attempt for
  // a machine already running the candidate, which correctly gets no
  // announcement at all. Either of the other words would assert a send, or a
  // rejection, that the hub is not in a position to know about.
  if (pending > 0) parts.push(`${pending} pending attempts`);
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

/**
 * The candidate this rollout is tracking, short form plus the full SHA.
 *
 * Null when there is nothing trustworthy to name — an unavailable entry has no
 * counts and no candidate, and inventing one would imply the page knows which
 * build the (absent) numbers describe.
 */
export function rolloutCandidateLabel(entry) {
  if (entry.state === ROLLOUT_UNAVAILABLE) return null;
  const full = entry.candidate || "";
  if (!/^[0-9a-f]{7,}$/i.test(full)) return null;
  return { short: full.slice(0, 7), full };
}

/**
 * The fleet-wide recovery action, and whether there is anything to recover.
 *
 * The operator pause and a platform halt are different things and were being
 * conflated. Before the automatic breaker was removed a halt usually arrived
 * alongside a pause, so "Resume" appeared; now a halt sets no fleet flag at
 * all, and a halted platform with the pause off offered the operator nothing
 * but a Pause button — the one control that cannot help.
 *
 * One endpoint covers both, and it is FLEET-WIDE: it clears the pause and
 * gives every halted platform a new attempt, in one transaction. The wording
 * must not imply the operator can retry one platform, because they cannot.
 *
 * An UNAVAILABLE entry is never retryable. The hub could not read that state,
 * so there is nothing to give an attempt to, and resume refuses outright when
 * the controller is missing — offering the button would promise a recovery
 * that cannot happen.
 */
export function rolloutRecoveryAction(entries, paused) {
  const halted = (entries ?? [])
    .filter((entry) => entry.state === ROLLOUT_HALTED)
    .map((entry) => entry.platform);
  const retryAvailable = halted.length > 0;
  let label = "Resume rollout";
  if (paused && retryAvailable) label = "Resume and retry halted rollouts";
  else if (!paused && retryAvailable) label = "Retry halted rollouts";
  return {
    halted,
    retryAvailable,
    label,
    detail: retryAvailable ? `Retries every halted platform: ${halted.join(", ")}` : "",
  };
}
