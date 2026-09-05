/**
 * AI Sessions — client-side contract types and pure display helpers.
 *
 * The types mirror proto/aisessions exactly. Everything the dashboard shows
 * about a session comes from these fields; there is nothing else to show.
 */

export type AITool = "claude" | "codex" | "kimi";

export type AIConfidence = "exact" | "inferred" | "unknown";

export interface AIAttr {
  value: string;
  source: string;
  confidence: AIConfidence | string;
}

export interface AISession {
  id: string;
  tool: AITool | string;
  started_at?: string;
  project: AIAttr;
  model: AIAttr;
  activity: AIAttr;
}

/** One machine's snapshot as delivered by GET /api/ai-sessions and SSE. */
export interface AISessionsMachineView {
  machine_id: string;
  hostname: string;
  received_at: string;
  sessions: AISession[];
}

export interface AISessionsResponse {
  enabled: boolean;
  stale_after_seconds: number;
  now?: string;
  machines: AISessionsMachineView[];
}

/** A machine snapshot re-keyed to the browser clock. */
export interface AISessionsMachine {
  machineId: string;
  hostname: string;
  sessions: AISession[];
  /** Browser-clock instant that corresponds to the hub's received_at. */
  receivedAtLocal: number;
}

export const TOOL_LABELS: Record<AITool, string> = {
  claude: "Claude Code",
  codex: "Codex",
  kimi: "Kimi",
};

export function toolLabel(tool: string): string {
  return TOOL_LABELS[tool as AITool] ?? tool;
}

/** Tailwind classes for the tool chip. Muted, consistent with badge palette. */
export function toolChipClass(tool: string): string {
  switch (tool) {
    case "claude":
      return "border-amber-500/30 bg-amber-500/10 text-amber-400";
    case "codex":
      return "border-emerald-500/30 bg-emerald-500/10 text-emerald-400";
    case "kimi":
      return "border-sky-500/30 bg-sky-500/10 text-sky-400";
    default:
      return "border-blox-border bg-blox-card text-blox-muted";
  }
}

/**
 * Format the time a process has been running. Coarse on purpose: a session
 * list is scanned, not read, and seconds-level churn is noise.
 */
export function formatRunningFor(startedAt: string | undefined, nowMs: number): string {
  if (!startedAt) return "unknown";
  const start = Date.parse(startedAt);
  if (!isFinite(start)) return "unknown";
  const sec = Math.max(0, Math.floor((nowMs - start) / 1000));
  if (sec < 60) return "<1m";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  const remMin = min % 60;
  if (hr < 24) return remMin ? `${hr}h ${remMin}m` : `${hr}h`;
  const day = Math.floor(hr / 24);
  const remHr = hr % 24;
  return remHr ? `${day}d ${remHr}h` : `${day}d`;
}

/** Seconds since the hub last heard from this machine's agent about sessions. */
export function snapshotAgeSeconds(receivedAtLocal: number, nowMs: number): number {
  return Math.max(0, Math.floor((nowMs - receivedAtLocal) / 1000));
}

export function isSnapshotStale(receivedAtLocal: number, nowMs: number, staleAfterSeconds: number): boolean {
  return snapshotAgeSeconds(receivedAtLocal, nowMs) > staleAfterSeconds;
}

/**
 * Convert a hub-clock received_at to a browser-clock instant. `hubNowMs` is
 * the hub clock at the moment the payload was produced and `localNowMs` the
 * browser clock when it arrived; the difference is the offset to apply so
 * ageing never depends on the two clocks agreeing.
 */
export function toLocalReceivedAt(receivedAt: string, hubNowMs: number, localNowMs: number): number {
  const received = Date.parse(receivedAt);
  if (!isFinite(received) || !isFinite(hubNowMs)) return localNowMs;
  return localNowMs - Math.max(0, hubNowMs - received);
}

/** Human labels for the attribute confidence marker. */
export function confidenceLabel(confidence: string): string {
  switch (confidence) {
    case "exact":
      return "exact";
    case "inferred":
      return "inferred";
    default:
      return "unknown";
  }
}

export function activityLabel(activity: AIAttr): string {
  if (activity.confidence === "unknown" || !activity.value) return "activity unknown";
  return `${activity.value} (inferred)`;
}

/** Total sessions across machines plus how many machines carry at least one. */
export function summarize(machines: Iterable<AISessionsMachine>): { sessions: number; machines: number; reporting: number } {
  let sessions = 0;
  let withSessions = 0;
  let reporting = 0;
  for (const m of machines) {
    reporting++;
    if (m.sessions.length > 0) {
      withSessions++;
      sessions += m.sessions.length;
    }
  }
  return { sessions, machines: withSessions, reporting };
}

/** Stable ordering: hostname, then machine id, then session start (oldest first). */
export function sortMachines(machines: AISessionsMachine[]): AISessionsMachine[] {
  return [...machines].sort((a, b) => {
    const h = (a.hostname || a.machineId).localeCompare(b.hostname || b.machineId);
    return h !== 0 ? h : a.machineId.localeCompare(b.machineId);
  });
}

export function sortSessions(sessions: AISession[]): AISession[] {
  return [...sessions].sort((a, b) => {
    const sa = a.started_at ? Date.parse(a.started_at) : Number.POSITIVE_INFINITY;
    const sb = b.started_at ? Date.parse(b.started_at) : Number.POSITIVE_INFINITY;
    if (sa !== sb) return sa - sb;
    return a.id.localeCompare(b.id);
  });
}
