import test from "node:test";
import assert from "node:assert/strict";
import { shouldLogoutOn401, storageAuthAction, tokenRemovalApplies } from "./auth-session.mjs";

test("401 logs out only when the failing request still carries the current token", () => {
  assert.equal(shouldLogoutOn401("tok-A", "tok-A"), true);
  // Older request racing a fresh login must not wipe the new session.
  assert.equal(shouldLogoutOn401("tok-old", "tok-new"), false);
  // No token on the request (already logged out) never logs out again.
  assert.equal(shouldLogoutOn401(null, null), false);
  assert.equal(shouldLogoutOn401("", null), false);
});

test("storage events map to logout or login-sync precisely", () => {
  assert.equal(storageAuthAction("bloxos_token", null), "logout");
  assert.equal(storageAuthAction(null, null), "logout"); // localStorage.clear()
  assert.equal(storageAuthAction("bloxos_token", "tok-new"), "sync-login");
  assert.equal(storageAuthAction("bloxos-preferences", null), null);
  assert.equal(storageAuthAction("bloxos-fleet-cache-v2-u1", null), null);
});

test("an old removal event never wipes a newer token", () => {
  assert.equal(tokenRemovalApplies(null), true);
  assert.equal(tokenRemovalApplies(""), true);
  assert.equal(tokenRemovalApplies("tok-new"), false);
});
