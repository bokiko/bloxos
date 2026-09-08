
// userIDFromToken reads user_id from a JWT payload without touching storage.
// The hub signs with user_id, not sub — see hub/auth.go.
export function decodeJWTPayload(token) {
  if (!token) return null;
  try {
    const parts = token.split(".");
    if (parts.length < 2) return null;
    const encoded = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const binary = atob(encoded.padEnd(Math.ceil(encoded.length / 4) * 4, "="));
    const payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(binary, c => c.charCodeAt(0))));
    return payload;
  } catch {
    return null;
  }
}
export function userIDFromToken(token) {
  const payload = decodeJWTPayload(token);
  return typeof payload?.user_id === "string" && payload.user_id !== "" ? payload.user_id : null;
}
// Auth session transition policy — pure decisions shared by AuthContext so
// the tricky cases (stale in-flight 401 vs. fresh login, cross-tab storage
// events) are unit-testable instead of living inside React callbacks.

// shouldLogoutOn401: a 401 only logs the user out when the request that
// failed still carries the CURRENTLY stored token. A request that started
// before a re-login (old token) must not wipe the new session.
export function shouldLogoutOn401(requestToken, currentToken) {
  return requestToken != null && requestToken !== "" && currentToken === requestToken;
}

// storageAuthAction decides what a storage event means for this tab:
//   "logout"     — the token was removed, or the whole store was cleared
//   "sync-login" — the token was replaced by a login in another tab; this
//                  tab must re-sync role/scopes/flags from storage
//   null         — unrelated change
export function storageAuthAction(eventKey, newValue) {
  if (eventKey === null) return "logout"; // localStorage.clear()
  if (eventKey !== "bloxos_token") return null;
  return newValue === null ? "logout" : "sync-login";
}

// tokenRemovalApplies: an old removal event must not wipe a newer login —
// only act when the store currently has no token.
export function tokenRemovalApplies(currentStoredToken) {
  return currentStoredToken == null || currentStoredToken === "";
}
