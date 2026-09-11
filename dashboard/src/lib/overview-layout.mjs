/* ============================================================================
 * What sits above the machine table.
 *
 * Pure, so it can be tested without a browser and so the component that owns
 * the grid does not also own the decisions. Nothing here fetches; everything
 * takes the data the page already has.
 *
 * The rule the whole file exists to keep: the grid is sized by what will
 * ACTUALLY be rendered, not by what the user selected. A module with nothing
 * honest to say returns null, and React cannot report that to its parent, so
 * `visibleOverviewModules` decides in the parent instead and the count follows
 * from it. Otherwise a selected-but-empty module leaves a hole in the layout.
 * ========================================================================== */

/** @typedef {"machine-first" | "balanced" | "power-focus"} OverviewLayout */
/** @typedef {{ availability: boolean, attention: boolean, urgent_alert: boolean }} OverviewWidgets */

export const OVERVIEW_LAYOUTS = /** @type {const} */ ([
  "machine-first",
  "balanced",
  "power-focus",
]);

export const DEFAULT_OVERVIEW_LAYOUT = "machine-first";

export const OVERVIEW_WIDGET_KEYS = /** @type {const} */ ([
  "availability",
  "attention",
  "urgent_alert",
]);

export const DEFAULT_OVERVIEW_WIDGETS = Object.freeze({
  availability: true,
  attention: true,
  urgent_alert: true,
});

export function isOverviewLayout(value) {
  return OVERVIEW_LAYOUTS.includes(value);
}

/**
 * Read path. Anything unrecognisable becomes the recommended set rather than
 * an empty overview — a corrupt cache should not look like a deliberate
 * choice to hide everything.
 */
export function normalizeOverviewWidgets(raw) {
  const source = raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {};
  return {
    availability: source.availability !== false,
    attention: source.attention !== false,
    urgent_alert: source.urgent_alert !== false,
  };
}

export function isDefaultOverview(layout, widgets) {
  if (layout !== DEFAULT_OVERVIEW_LAYOUT) return false;
  return OVERVIEW_WIDGET_KEYS.every((key) => widgets?.[key] === DEFAULT_OVERVIEW_WIDGETS[key]);
}

/**
 * The one alert worth putting a name to: highest severity first, then most
 * recent. The hub returns active alerts newest-first and emits only "warning"
 * and "critical", so array order is the tiebreak and no third severity can
 * quietly sort to the top.
 *
 * Returns null when there is nothing real to show — the caller renders nothing
 * rather than an empty card.
 */
export function urgentAlert(alerts) {
  if (!Array.isArray(alerts)) return null;
  let best = null;
  for (const alert of alerts) {
    if (!alert || typeof alert.id !== "string" || alert.id === "") continue;
    if (best === null) {
      best = alert;
      continue;
    }
    if (alert.severity === "critical" && best.severity !== "critical") best = alert;
  }
  return best;
}

/** Counts for the attention module. Only the two severities the hub emits. */
export function alertBreakdown(alerts) {
  const list = Array.isArray(alerts) ? alerts.filter((a) => a && typeof a.id === "string" && a.id !== "") : [];
  let critical = 0;
  let warning = 0;
  for (const alert of list) {
    if (alert.severity === "critical") critical += 1;
    else warning += 1;
  }
  return { total: list.length, critical, warning };
}

/**
 * The modules that will actually render, in display order.
 *
 * `ready` is "the fleet data has arrived at least once" — before that even the
 * availability numbers would be a guess, so the stack stays empty rather than
 * flashing zeros. Power focus deliberately shows no modules at all; the
 * critical/offline marker it still owes the operator is the workspace's job,
 * not a module's.
 */
export function visibleOverviewModules(layout, widgets, data) {
  if (layout === "power-focus") return [];
  if (!data?.ready) return [];
  const enabled = normalizeOverviewWidgets(widgets);
  const modules = [];
  if (enabled.availability) modules.push("availability");
  if (enabled.attention) modules.push("attention");
  // The urgent line names one real alert. With no active alert there is
  // nothing to name, so the module is absent and the grid closes up.
  if (enabled.urgent_alert && urgentAlert(data.alerts) !== null) modules.push("urgent_alert");
  return modules;
}

/**
 * The one-line fleet state, built only from counts that were measured.
 * Numbers joined by " · " — never a second clause, never a mood.
 */
export function healthLine(counts) {
  if (!counts || !Number.isFinite(counts.total) || counts.total === 0) return "No machines enrolled";
  const { total, live = 0, needsReview = 0, offline = 0 } = counts;
  if (live === total) return `All ${total} machines live`;
  const parts = [`${live} of ${total} live`];
  if (needsReview > 0) parts.push(`${needsReview} need${needsReview === 1 ? "s" : ""} review`);
  if (offline > 0) parts.push(`${offline} offline`);
  return parts.join(" · ");
}

/** The dot beside the health line. Worst real state wins. */
export function healthTone(counts) {
  if (!counts || !Number.isFinite(counts.total) || counts.total === 0) return "neutral";
  if ((counts.critical ?? 0) > 0 || (counts.offline ?? 0) > 0) return "critical";
  if ((counts.warning ?? 0) > 0 || (counts.stale ?? 0) > 0) return "warning";
  return "ok";
}
