// Unified updater status logic for the Settings → Updates UI. Pure helpers so
// downtime-tolerant polling and button gating are unit-testable.

export const ACTIVE_STATES = [
  "checking", "staging", "backing_up", "installing", "verifying", "rolling_back",
];
export const TERMINAL_STATES = ["succeeded", "rolled_back", "failed"];

export function isActiveState(state) {
  return ACTIVE_STATES.includes(state);
}

// canRequestUpdate: the button is only ever enabled when the updater is
// configured and no update is in flight. Never enables on unknown/unavailable.
export function canRequestUpdate(resp) {
  if (!resp || resp.available !== true) return false;
  const state = resp.status && resp.status.state;
  return !state || !isActiveState(state);
}

const STATE_LABELS = {
  idle: "Idle",
  checking: "Checking for release",
  staging: "Staging release",
  backing_up: "Backing up",
  installing: "Installing",
  verifying: "Verifying",
  succeeded: "Update complete",
  rolling_back: "Rolling back",
  rolled_back: "Rolled back to previous build",
  failed: "Update failed",
};

export function statusLabel(state) {
  return STATE_LABELS[state] || state || "Unknown";
}

// pollPhase computes what the UI should show across polls, tolerating the
// expected hub restart mid-update: a fetch error while active means
// "reconnecting" and preserves the last known state instead of faking an
// outcome. previous is the last computed phase object
// ({phase, state, message}). requestID (when given) is the id of the request
// this UI submitted — a terminal status belonging to a DIFFERENT request is
// stale history and must never be shown as our update succeeding.
/** @param {string|null} requestID */
export function pollPhase(previous, resp, fetchFailed, requestID = null) {
  if (fetchFailed) {
    if (previous && isActiveState(previous.state)) {
      return { phase: "reconnecting", state: previous.state, message: previous.message };
    }
    return { phase: "unreachable", state: previous ? previous.state : null, message: previous ? previous.message : "" };
  }
  if (!resp || resp.available !== true) {
    return { phase: "unavailable", state: null, message: (resp && resp.reason) || "updater not configured" };
  }
  const status = resp.status || {};
  if (requestID && TERMINAL_STATES.includes(status.state) && status.request_id !== requestID) {
    // A different request's terminal result: ours is still being picked up.
    if (previous && (previous.phase === "active" || previous.phase === "reconnecting" || previous.phase === "accepted")) {
      return { phase: previous.phase === "reconnecting" ? "reconnecting" : "accepted", state: previous.state, message: previous.message };
    }
    return { phase: "accepted", state: "idle", message: "request sent; waiting for the worker to start" };
  }
  const state = status.state || "idle";
  if (state === "succeeded") return { phase: "succeeded", state, message: status.message || "" };
  if (state === "failed") return { phase: "failed", state, message: status.message || "" };
  if (state === "rolled_back") return { phase: "rolled_back", state, message: status.message || "" };
  if (isActiveState(state)) return { phase: "active", state, message: status.message || "" };
  return { phase: "idle", state, message: status.message || "" };
}
