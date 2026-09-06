// Match the hub's 15-minute install-token TTL. This caps display wording
// only; the server's expires_at still determines whether a token is expired.
/** @param {number} remainingMs */
export function installTokenMinutesRemaining(remainingMs) {
  return Math.min(15, Math.max(0, Math.ceil(remainingMs / 60_000)));
}
