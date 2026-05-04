/**
 * Parse a server-side timestamp into epoch milliseconds.
 *
 * Handles the timestamp formats the hub emits over the SSE stream:
 *   1. RFC3339 / ISO 8601              — "YYYY-MM-DDTHH:MM:SS.nnnZ" (agent-emitted)
 *   2. SQLite CURRENT_TIMESTAMP        — "YYYY-MM-DD HH:MM:SS" (UTC, no zone)
 *   3. Go time.Time.String() via SQL   — "YYYY-MM-DD HH:MM:SS.nnnnnnnnn +0000 UTC"
 *
 * Returns 0 for any unparseable input. Callers MUST treat 0 as "unknown / never";
 * never substitute Date.now() — that's the bug this helper exists to prevent.
 */
export function parseServerTimestamp(raw: string | number | undefined | null): number {
  if (typeof raw === "number") return raw;
  if (!raw) return 0;

  const normalized = raw
    .replace(" +0000 UTC", "")
    .replace(" UTC", "")
    .replace(" ", "T")
    .replace(/Z?$/, "Z");

  const parsed = Date.parse(normalized);
  return isNaN(parsed) ? 0 : parsed;
}
