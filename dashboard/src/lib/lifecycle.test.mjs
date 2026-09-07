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
  assert.match(auth, /e\.key === "bloxos_token" && e\.newValue === null[\s\S]*?logout\(\)/, "token removal in another tab must log this tab out");
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
