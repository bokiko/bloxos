// Per-user preferences cache. The pre-fix cache used one global
// "bloxos-preferences" key, so a logout → different-user login painted the
// previous user's display name, pins and filters until the network answered
// (and indefinitely when it failed). Keys are now per user and the legacy
// unscoped entry is purged, never migrated into another user's session.

const LEGACY_KEY = "bloxos-preferences";
const KEY_PREFIX = "bloxos-preferences-u-";

export function preferencesCacheKey(userID) {
  return KEY_PREFIX + (userID ?? "anon");
}

// readPreferencesCache returns only this user's cached preferences. A null
// userID (logged out) reads nothing. Malformed entries are dropped.
export function readPreferencesCache(userID, normalize) {
  if (typeof window === "undefined" || !userID) return null;
  try {
    const raw = localStorage.getItem(preferencesCacheKey(userID));
    if (!raw) return null;
    return normalize(JSON.parse(raw));
  } catch {
    return null;
  }
}

export function writePreferencesCache(userID, prefs) {
  if (typeof window === "undefined" || !userID) return;
  try {
    localStorage.setItem(preferencesCacheKey(userID), JSON.stringify(prefs));
  } catch {
    // Quota exceeded — the next refresh rebuilds from server state.
  }
}

// purgeLegacyPreferencesCache removes the unscoped pre-fix entry. Its
// content is never read into any user's session — deleting it is the only
// safe disposition.
export function purgeLegacyPreferencesCache() {
  if (typeof window === "undefined") return;
  try {
    localStorage.removeItem(LEGACY_KEY);
  } catch {
    // ignore
  }
}
