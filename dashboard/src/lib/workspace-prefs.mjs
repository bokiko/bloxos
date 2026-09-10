// Per-user Overview workspace state: which sections the operator has
// collapsed, which metric the load ranking is showing, and which machines are
// marked "expected high load".
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

/** The Overview sections an operator can collapse. Order is display order. */
export const OVERVIEW_SECTIONS = ["availability", "attention", "capacity", "load", "fleet"];

/** Metrics the "Highest load" ranking can sort by. `cpu` is the default and
 * the behaviour that shipped. */
export const LOAD_METRICS = ["cpu", "gpu", "memory"];

export const DEFAULT_WORKSPACE_PREFS = Object.freeze({
  /** Section ids from OVERVIEW_SECTIONS that are currently collapsed. */
  collapsed: [],
  /** One of LOAD_METRICS. */
  load_metric: "cpu",
  /** Machine ids whose CPU saturation is their normal working state. */
  expected_high_cpu: [],
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
  const metric = LOAD_METRICS.includes(r.load_metric) ? r.load_metric : DEFAULT_WORKSPACE_PREFS.load_metric;
  return {
    collapsed: stringList(r.collapsed, OVERVIEW_SECTIONS),
    load_metric: metric,
    // Machine ids are opaque and are NOT validated against the live fleet: a
    // machine that is briefly absent from the SSE stream must not silently
    // lose its flag. Stale ids are inert.
    expected_high_cpu: stringList(r.expected_high_cpu, null),
  };
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
