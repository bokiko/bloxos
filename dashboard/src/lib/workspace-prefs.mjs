// Per-user Overview workspace state: which sections the operator has
// collapsed, which window and tariff the fleet power pane is using, and which
// machines are marked "expected high load".
//
// WHY THIS IS NOT A SERVER PREFERENCE
// GET/PATCH /api/me/preferences returns a fixed Go struct (hub/preferences.go)
// whose scalar fields are named columns on `users`. Every new field there is a
// schema migration plus a hub release, and these three are view state for one
// browser profile. So they live in localStorage, keyed per user exactly the
// way lib/preferences-cache.mjs keys the preferences cache — a logged-out or
// switched user never inherits the previous user's workspace. The cost is
// honest and small: this state does not follow a user to another device.
//
// Every storage access is wrapped. Safari private mode throws on the first
// localStorage touch and a quota failure must never take the Overview down.

const KEY_PREFIX = "bloxos-workspace-u-";

/**
 * The Overview no longer folds anything, so there are no collapsible sections
 * left to remember. `collapsed` is deliberately still ACCEPTED and dropped on
 * read (see normalizeWorkspacePrefs): every operator who ever folded a pane has
 * the key sitting in localStorage, and a normalizer that threw or preserved it
 * would either break their workspace or carry a preference nothing reads.
 */

/** Windows the fleet power pane can chart. They mirror the hub's `?period`
 * vocabulary; 7d is absent because power history is retained for 24 hours. */
export const POWER_PERIODS = ["30m", "1h", "6h", "24h"];

/** Currencies the electricity tariff can be quoted in. A closed list because
 * the code is handed to Intl.NumberFormat, which throws on a bad one. */
export const POWER_CURRENCIES = [
  "USD", "EUR", "GBP", "CAD", "AUD", "CHF",
  "SEK", "NOK", "DKK", "PLN", "CZK", "TRY",
  "JPY", "CNY", "INR", "BRL", "ZAR", "AED",
];

/** A tariff above this per kWh is a typo, not a price. */
export const POWER_MAX_RATE = 100;

export const DEFAULT_WORKSPACE_PREFS = Object.freeze({
  /** Machine ids whose CPU saturation is their normal working state. */
  expected_high_cpu: [],
  /** One of POWER_PERIODS. */
  power_period: "6h",
  /**
   * The electricity tariff the fleet power pane costs energy at.
   *
   * `per_kwh` is null until the operator states one, and that is deliberate:
   * a "typical" default rate would print a confident, specific and completely
   * invented number next to real watts. No rate, no cost — the pane asks.
   */
  power_rate: Object.freeze({ currency: "USD", per_kwh: null }),
});

export function workspacePrefsKey(userID) {
  return KEY_PREFIX + (userID ?? "anon");
}

/** Drop anything that is not a non-empty string, de-duplicate, keep order. */
function stringList(raw, allowed) {
  if (!Array.isArray(raw)) return [];
  const out = [];
  for (const value of raw) {
    if (typeof value !== "string" || value === "") continue;
    if (allowed && !allowed.includes(value)) continue;
    if (!out.includes(value)) out.push(value);
  }
  return out;
}

/**
 * Coerce whatever is in storage into a usable shape. A corrupt or
 * partially-written entry degrades to the defaults for the fields it broke
 * rather than throwing the whole workspace away.
 */
export function normalizeWorkspacePrefs(raw) {
  const r = raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {};
  return {
    // `collapsed` is intentionally not carried through: the sections it named
    // are gone, so a stored value is read, ignored and never written back.
    // Machine ids are opaque and are NOT validated against the live fleet: a
    // machine that is briefly absent from the SSE stream must not silently
    // lose its flag. Stale ids are inert.
    expected_high_cpu: stringList(r.expected_high_cpu, null),
    power_period: POWER_PERIODS.includes(r.power_period)
      ? r.power_period
      : DEFAULT_WORKSPACE_PREFS.power_period,
    power_rate: normalizePowerRate(r.power_rate),
  };
}

/**
 * A tariff, coerced. An unusable rate becomes null rather than 0: zero is a
 * price ("my power is free"), and silently claiming it would put a confident
 * cost of nothing under a chart of real watts.
 */
export function normalizePowerRate(raw) {
  const r = raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {};
  const currency = POWER_CURRENCIES.includes(r.currency)
    ? r.currency
    : DEFAULT_WORKSPACE_PREFS.power_rate.currency;
  const usable =
    typeof r.per_kwh === "number" &&
    Number.isFinite(r.per_kwh) &&
    r.per_kwh >= 0 &&
    r.per_kwh <= POWER_MAX_RATE;
  return { currency, per_kwh: usable ? r.per_kwh : null };
}

/** This user's workspace state. A null userID (logged out) reads nothing. */
export function readWorkspacePrefs(userID) {
  if (typeof window === "undefined" || !userID) return normalizeWorkspacePrefs(null);
  try {
    const raw = localStorage.getItem(workspacePrefsKey(userID));
    if (!raw) return normalizeWorkspacePrefs(null);
    return normalizeWorkspacePrefs(JSON.parse(raw));
  } catch {
    return normalizeWorkspacePrefs(null);
  }
}

export function writeWorkspacePrefs(userID, prefs) {
  if (typeof window === "undefined" || !userID) return;
  try {
    localStorage.setItem(workspacePrefsKey(userID), JSON.stringify(normalizeWorkspacePrefs(prefs)));
  } catch {
    // Quota exceeded or storage blocked — the workspace stays correct for this
    // session and simply does not survive the reload.
  }
}

/** Add or remove one member, returning a new array. */
export function toggleMember(list, value) {
  const current = Array.isArray(list) ? list : [];
  return current.includes(value) ? current.filter((v) => v !== value) : [...current, value];
}
