import test from "node:test";
import assert from "node:assert/strict";

// Synthetic browser storage for the pure cache module (node has neither).
const store = new Map();
globalThis.window = {};
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};

const { readPreferencesCache, writePreferencesCache, purgeLegacyPreferencesCache } =
  await import("./preferences-cache.mjs");

const identity = (x) => x;

test("preferences cache is isolated per user", () => {
  store.clear();
  writePreferencesCache("admin1", { display_name: "ADMIN-PRIVATE-TEST" });
  // Same user reads it back.
  assert.equal(readPreferencesCache("admin1", identity)?.display_name, "ADMIN-PRIVATE-TEST");
  // A different user (viewer after logout) never sees it.
  assert.equal(readPreferencesCache("viewer1", identity), null);
  // Logged-out state reads nothing.
  assert.equal(readPreferencesCache(null, identity), null);
});

test("legacy unscoped cache is purged and never read into a session", () => {
  store.clear();
  store.set("bloxos-preferences", JSON.stringify({ display_name: "LEGACY-LEAK" }));
  purgeLegacyPreferencesCache();
  assert.equal(store.has("bloxos-preferences"), false);
  // Even if something re-created it, reads are keyed per user.
  store.set("bloxos-preferences", JSON.stringify({ display_name: "LEGACY-LEAK" }));
  assert.equal(readPreferencesCache("admin1", identity), null);
  store.clear();
});

test("null user writes nothing", () => {
  store.clear();
  writePreferencesCache(null, { display_name: "x" });
  assert.equal(store.size, 0);
});
