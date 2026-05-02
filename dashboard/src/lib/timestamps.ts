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
 * (See dashboard staleness postmortem in HANDOFF.md.)
 */
export function parseServerTimestamp(raw: string | number | undefined | null): number {
  if (typeof raw === "number") return raw;
  if (!raw) return 0;

  // Try direct JS Date parse first — handles RFC3339/ISO 8601 natively.
  const direct = Date.parse(raw);
  if (!isNaN(direct)) return direct;

  // Fall back to Go/SQLite "YYYY-MM-DD HH:MM:SS[.fff] [+0000 UTC]" normalization.
  const isoish = raw
    .replace(" +0000 UTC", "")
    .replace(" UTC", "")
    .replace(" ", "T") + "Z";
  const fallback = Date.parse(isoish);
  return isNaN(fallback) ? 0 : fallback;
}
