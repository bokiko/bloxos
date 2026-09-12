/**
 * One freshness policy for every power readout in the product.
 *
 * Three different rules used to coexist: PowerHistory treated 90s as current,
 * a proposed fleet table used 150s, and the fleet pane's `latestReading`
 * walked back through a whole 24-hour window and headlined the last non-null
 * value with no age shown at all. A fleet that went dark at 02:00 could
 * therefore show dashes in one panel and "142 W measured" in another, side by
 * side on the same screen. That is the alert-count disagreement rebuilt: two
 * views of one fact, disagreeing, with nothing to say which was right.
 *
 * So the policy lives here, once, and every surface imports it.
 *
 * Three rules the callers must honour:
 *
 *   1. AGE COMES FROM AN OBSERVATION, NEVER FROM A CHART BIN.
 *      A bucket's `end_unix_ms` is an axis coordinate. The last bucket of a
 *      24h view ends up to 15 minutes in the FUTURE, so measuring age from it
 *      makes stale data look current. Age must be measured from the agent's
 *      own window end (`latest_observation_end_unix_ms`).
 *
 *   2. ONE FRESH CONTRIBUTOR DOES NOT REFRESH THE REST.
 *      An aggregate is only as current as its OLDEST contributor. Six machines
 *      where five went dark an hour ago and one still reports is not a fresh
 *      fleet reading; it is one machine's reading wearing a fleet's label.
 *      `aggregateFreshness` therefore takes the worst contributor, not the best.
 *
 *   3. NEGATIVE AGE IS NOT FRESHNESS.
 *      A clock running ahead produces an observation "in the future". That is
 *      unknown, not current — a badly skewed agent would otherwise read as
 *      permanently up to date.
 */

/**
 * How long a power observation stays current.
 *
 * The agent emits a 30-second window, so anything under one window has simply
 * not been superseded yet. Five windows allows for a missed report and the
 * hub's own aggregation lag without declaring a working machine stale.
 */
export const POWER_FRESH_MS = 150_000;

/**
 * How far ahead of our clock an observation may be stamped and still be taken
 * at face value. Deliberately small: this is timestamp resolution and transit
 * jitter, NOT a clock-drift allowance. A reading cannot be newer than now, so
 * anything meaningfully ahead is skew of unknown size — and unknown age must
 * never render as a current reading. Matches the hub's
 * fleetPowerFutureSkewToleranceMS.
 */
export const POWER_FUTURE_SKEW_TOLERANCE_MS = 2_000;

/** Freshness states. `unavailable` means nothing ever reported. */
export const FRESH = "fresh";
export const STALE = "stale";
export const SKEWED = "skewed";
export const UNAVAILABLE = "unavailable";

/**
 * Classify one observation.
 *
 * `observedEndMS` must be an agent window end, not a bucket edge. Returns
 * `{ state, ageMS }`; `ageMS` is null when there is nothing to age.
 */
export function freshnessOf(observedEndMS, nowMS) {
  if (!Number.isFinite(observedEndMS) || observedEndMS <= 0) {
    return { state: UNAVAILABLE, ageMS: null };
  }
  if (!Number.isFinite(nowMS)) return { state: UNAVAILABLE, ageMS: null };
  const ageMS = nowMS - observedEndMS;
  if (ageMS < -POWER_FUTURE_SKEW_TOLERANCE_MS) {
    // Ahead of our clock by more than tolerance: we cannot say how old this is.
    return { state: SKEWED, ageMS };
  }
  if (ageMS <= POWER_FRESH_MS) return { state: FRESH, ageMS: Math.max(0, ageMS) };
  return { state: STALE, ageMS };
}

/**
 * Classify an aggregate from its contributors' observation ends.
 *
 * The aggregate is as fresh as its OLDEST contributor, which is why this takes
 * the minimum rather than the maximum. A single still-reporting machine must
 * not make five dark ones look current.
 *
 * Any contributor whose clock is skewed makes the aggregate's age unknowable,
 * so the whole aggregate reports `skewed` rather than a confident number.
 */
export function aggregateFreshness(observedEndsMS, nowMS) {
  const ends = (observedEndsMS ?? []).filter((v) => Number.isFinite(v) && v > 0);
  if (ends.length === 0) return { state: UNAVAILABLE, ageMS: null, contributors: 0 };

  let oldest = Infinity;
  let anySkew = false;
  for (const end of ends) {
    const { state } = freshnessOf(end, nowMS);
    if (state === SKEWED) anySkew = true;
    if (end < oldest) oldest = end;
  }
  if (anySkew) {
    return { state: SKEWED, ageMS: nowMS - oldest, contributors: ends.length };
  }
  const worst = freshnessOf(oldest, nowMS);
  return { ...worst, contributors: ends.length };
}

/** True only when a readout may be presented as the current value. */
export function isCurrent(freshness) {
  return freshness?.state === FRESH;
}

/**
 * Short human age, e.g. "8s", "4m", "3h". Null when there is nothing to say.
 * Callers print this beside any value that is not current, so a stale number is
 * never shown without its age.
 */
export function formatAge(ageMS) {
  if (!Number.isFinite(ageMS)) return null;
  if (ageMS < 0) return null; // skew: the caller says "clock ahead", not an age
  const seconds = Math.floor(ageMS / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/**
 * The words to put beside a reading, given its freshness. Never returns a bare
 * number: a value that is not current always carries why.
 */
export function freshnessNote(freshness) {
  switch (freshness?.state) {
    case FRESH:
      return null;
    case STALE: {
      const age = formatAge(freshness.ageMS);
      return age ? `last reported ${age} ago` : "no recent report";
    }
    case SKEWED:
      return "clock ahead of hub — age unknown";
    default:
      return "no reading";
  }
}
