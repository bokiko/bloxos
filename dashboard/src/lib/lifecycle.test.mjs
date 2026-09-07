import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// Wiring guards for the lifecycle fixes. These read the real sources and
// assert the shape of each fix; they are regression tripwires, not proof of
// rendered behavior (browser acceptance is owned separately).

const pageDetail = readFileSync(new URL("../app/machine/[id]/page.tsx", import.meta.url), "utf8");
const fleetPage = readFileSync(new URL("../app/page.tsx", import.meta.url), "utf8");
const sse = readFileSync(new URL("../contexts/SSEContext.tsx", import.meta.url), "utf8");
const auth = readFileSync(new URL("../contexts/AuthContext.tsx", import.meta.url), "utf8");
const services = readFileSync(new URL("../components/ServicePanel.tsx", import.meta.url), "utf8");
const containers = readFileSync(new URL("../components/ContainerPanel.tsx", import.meta.url), "utf8");

test("terminal PIN entry is not capped below what setup/change-PIN allow", () => {
  const i = pageDetail.indexOf("ref={pinInputRef}");
  assert.ok(i > 0, "PIN input not found");
  const block = pageDetail.slice(i, pageDetail.indexOf("/>", i));
  assert.doesNotMatch(block, /maxLength/, "terminal PIN input still caps length (valid long PINs become unenterable)");
});

test("detail status shares the fleet freshness policy and gates controls on it", () => {
  assert.match(pageDetail, /detailStatus\(\{[\s\S]*?lastSeenMs: parseServerTimestamp/, "getStatus must delegate to detailStatus");
  assert.match(pageDetail, /getStatus\(data, now\)/, "status must be computed against the ticking clock");
  assert.match(pageDetail, /isOnline = status === "live" \|\| status === "warning"/, "controls must follow the freshness-aware status");
});

test("grid delete cleans up in finally and reports failures", () => {
  const m = fleetPage.match(/const handleDeleteFromGrid = useCallback\(async[\s\S]*?\}, \[/);
  assert.ok(m, "handleDeleteFromGrid not found");
  assert.match(m[0], /if \(!res\.ok\) \{\s*addToast\("error"/, "failed delete must toast an error");
  assert.match(m[0], /finally \{[\s\S]*?setDeleteLoading\(false\)[\s\S]*?setDeleteTarget\(null\)/, "dialog must reset in finally");
});

test("machine_removed SSE event removes the machine from fleet state", () => {
  assert.match(sse, /addEventListener\("machine_removed"/, "SSEContext must subscribe to machine_removed");
  assert.match(sse, /"machine_removed"[\s\S]*?next\.delete\(msg\.machine_id\)/, "handler must delete by machine_id");
});

test("logout cancels pending cache writes before clearing", () => {
  const logoutBranch = sse.match(/\} else \{[\s\S]*?disconnect\(true\);\s*\}/);
  assert.ok(logoutBranch, "logout branch not found");
  assert.ok(
    logoutBranch[0].indexOf("cacheCancelRef.current?.()") < logoutBranch[0].indexOf("clearCache("),
    "pending writer must be cancelled before clearCache",
  );
  assert.match(sse, /cacheCancelRef\.current = cancel/, "writer cancel must be captured on user change");
});

test("cross-tab logout clears this tab's auth state", () => {
  assert.match(auth, /addEventListener\("storage"/, "AuthContext must listen for storage events");
  assert.match(auth, /storageAuthAction\(e\.key, e\.newValue\)/, "storage events must go through the shared policy");
  assert.match(auth, /action === "logout"[\s\S]*?logout\(\)/, "token removal or full clear must log this tab out");
  assert.match(auth, /action === "sync-login"[\s\S]*?setRole[\s\S]*?setScopes/, "token replacement must re-sync role and scopes");
});

test("logout invalidates in-flight connects and stale EventSources are guarded", () => {
  const logoutBranch = sse.match(/\} else \{[\s\S]*?disconnect\(true\);\s*\}/);
  assert.ok(logoutBranch, "logout branch not found");
  assert.match(logoutBranch[0], /connectSeqRef\.current\+\+/, "logout must invalidate in-flight connect generations");
  assert.match(sse, /addEventListener\("metrics"[\s\S]*?esRef\.current !== es/, "metrics handler must reject a replaced EventSource");
  assert.match(sse, /addEventListener\("snapshot"[\s\S]*?esRef\.current !== es/, "snapshot handler must reject a replaced EventSource");
  assert.match(sse, /es\.onopen = \(\) => \{[\s\S]*?esRef\.current !== es/, "onopen must reject a replaced EventSource");
  assert.match(sse, /es\.onerror = \(\) => \{[\s\S]*?esRef\.current !== es/, "onerror must reject a replaced EventSource");
});

test("machine_removed persists the filtered cache and alerts fetch re-checks the token", () => {
  assert.match(sse, /"machine_removed"[\s\S]*?next\.delete\(msg\.machine_id\)[\s\S]*?cacheFlushRef\.current\?\.\(\)/, "removal must flush the filtered cache");
  assert.match(sse, /getStoredToken\(\) === token/, "initial alerts fetch must re-validate the token before applying");
});

test("401 logout requires the failing request to carry the current token", () => {
  assert.match(auth, /res\.status === 401 && shouldLogoutOn401\(currentToken/, "401 must be gated on token currency");
});

test("login publishes role/scopes/flags before the token and clears stale flags", () => {
  const m = auth.match(/const login = useCallback\(async[\s\S]*?\}, \[\]\)/);
  assert.ok(m, "login not found");
  assert.match(m[0], /sessionGenRef/, "login must carry a session generation");
  assert.match(m[0], /gen !== sessionGenRef\.current/, "in-flight login must bail when superseded");
  const scopesAt = m[0].indexOf('localStorage.setItem("bloxos_scopes"');
  const tokenAt = m[0].indexOf('localStorage.setItem("bloxos_token"');
  assert.ok(scopesAt > 0 && tokenAt > scopesAt, "token must be published after role/scopes/flags");
  assert.match(m[0], /localStorage\.removeItem\("bloxos_pw_change_required"\)/, "login must clear a false pw-change flag");
  assert.match(m[0], /localStorage\.removeItem\("bloxos_pin_change_required"\)/, "login must clear a false pin-change flag");
});

test("storage logout reads the current snapshot and skips newer tokens", () => {
  assert.match(auth, /tokenRemovalApplies\(getStoredToken\(\)\)/, "removal must be gated on the current store");
  assert.match(auth, /const storedToken = getStoredToken\(\)/, "sync-login must read the stored token, not the event payload");
});

test("preferences are keyed per user with guarded async mutations", () => {
  const prefs = readFileSync(new URL("../contexts/PreferencesContext.tsx", import.meta.url), "utf8");
  assert.match(prefs, /useMemo<string \| null>\(\(\) => userIDFromToken\(token\), \[token\]\)/, "userID must track the token, not just isAuthenticated");
  assert.doesNotMatch(prefs, /isAuthenticated/, "preferences must not key off the boolean auth flag");
  assert.match(prefs, /purgeLegacyPreferencesCache\(\)/, "legacy unscoped cache must be purged");
  assert.match(prefs, /readPreferencesCache\(userID, normalizePreferences\)/, "hydration must read only the current user's keyed cache");
  // Every async mutation must capture uid at entry and gate state writes on it.
  for (const fn of ["refresh", "updateScalar", "uploadAvatar", "removeAvatar", "pinMachine", "unpinMachine", "saveFilter", "deleteFilter"]) {
    const m = prefs.match(new RegExp("const " + fn + " = useCallback\\(\\s*async[\\s\\S]*?\\},\\s*\\["));
    assert.ok(m, `${fn} not found`);
    assert.match(m[0], /userIDRef\.current/, `${fn} must consult the user ref`);
  }
  for (const fn of ["updateScalar", "uploadAvatar", "removeAvatar", "saveFilter"]) {
    const m = prefs.match(new RegExp("const " + fn + " = useCallback\\(\\s*async[\\s\\S]*?\\},\\s*\\["));
    assert.match(m[0], /userIDRef\.current !== uid/, `${fn} must skip state writes after a user switch`);
  }
});

test("machine card keyboard handler does not hijack action buttons", () => {
  // Browser repro: focusing a nested Delete button and pressing Enter used
  // to navigate instead of activating it — the card's keydown ran on the
  // bubbled event and its preventDefault cancelled the native button click.
  const card = readFileSync(new URL("../components/MachineCard.tsx", import.meta.url), "utf8");
  const m = card.match(/onKeyDown=\{\(e\) => \{[\s\S]*?\}\}/);
  assert.ok(m, "card keydown handler not found");
  assert.match(m[0], /e\.target !== e\.currentTarget/, "bubbled events from child buttons must be ignored");
});

test("detail-page delete keeps the dialog retryable and shows the failure", () => {
  const m = pageDetail.match(/const handleDeleteMachine = useCallback\(async[\s\S]*?\}, \[/);
  assert.ok(m, "handleDeleteMachine not found");
  assert.match(m[0], /finally \{[\s\S]*?setDeleting\(false\)/, "loading must reset in finally");
  assert.match(m[0], /setDeleteError\(/, "failure must surface an error");
  assert.doesNotMatch(m[0], /else \{[\s\S]*?setShowDeleteConfirm\(false\)/, "failure must not close the dialog silently");
  assert.match(pageDetail, /deleteError && <p role="alert"/, "delete error must be rendered");
});

test("login labels are bound to their inputs", () => {
  const login = readFileSync(new URL("../app/login/page.tsx", import.meta.url), "utf8");
  assert.match(login, /<label htmlFor="login-username"/, "username label must be bound");
  assert.match(login, /id="login-username"/, "username input must carry the id");
  assert.match(login, /<label htmlFor="login-password"/, "password label must be bound");
  assert.match(login, /id="login-password"/, "password input must carry the id");
});

test("service and container controls are hidden from viewers", () => {
  for (const [name, src] of [["ServicePanel", services], ["ContainerPanel", containers]]) {
    assert.match(src, /hasScope\("fleet\.control"\)/, `${name} must check fleet.control`);
    assert.match(src, /if \(!canControl\) \{\s*return null;\s*\}/, `${name} must render no actions for viewers`);
  }
});

test("terminal start surfaces actionable hub errors and copy is awaited", () => {
  assert.match(pageDetail, /terminalStartError\(res\.status, hubError\)/, "non-403 terminal failures must use actionable messages");
  assert.match(pageDetail, /termError && /, "terminal error must be rendered");
  assert.match(pageDetail, /await navigator\.clipboard\.writeText/, "copy must be awaited");
  assert.match(pageDetail, /Copy failed — select and copy the text manually/, "copy failure must not claim success");
});
