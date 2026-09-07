import test, { mock } from "node:test";
import assert from "node:assert/strict";

// Synthetic browser storage for the pure cache module (node has neither).
const store = new Map();
globalThis.window = {};
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => store.set(k, String(v)),
  removeItem: (k) => store.delete(k),
};

const { makeDebouncedWriter, clearCache, readCache } = await import("./metrics-cache.ts");

function machines() {
  return [{ machine_id: "m1", cpu_percent: 1 }];
}

test("cancel() prevents a pending write from resurrecting a cleared cache", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const [writer, , cancel] = makeDebouncedWriter("u1");
    writer(machines());
    // Logout order: cancel the pending write, THEN clear. After the debounce
    // window the cache must still be gone.
    cancel();
    clearCache("u1");
    mock.timers.tick(10_000);
    assert.equal(readCache("u1"), null);
  } finally {
    mock.timers.reset();
    store.clear();
  }
});

test("flush() writes immediately; a later timer is inert", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const [writer, flush] = makeDebouncedWriter("u1");
    writer(machines());
    flush();
    assert.equal(readCache("u1")?.[0]?.machine_id, "m1");
    mock.timers.tick(10_000); // no double write / no throw
    assert.equal(readCache("u1")?.length, 1);
  } finally {
    mock.timers.reset();
    store.clear();
  }
});

test("an uncancelled writer still persists after the debounce window", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const [writer] = makeDebouncedWriter("u1");
    writer(machines());
    mock.timers.tick(2_100);
    assert.equal(readCache("u1")?.[0]?.machine_id, "m1");
  } finally {
    mock.timers.reset();
    store.clear();
  }
});

test("cancel() with nothing pending is a no-op", () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    const [, , cancel] = makeDebouncedWriter("u1");
    cancel();
    mock.timers.tick(10_000);
    assert.equal(readCache("u1"), null);
  } finally {
    mock.timers.reset();
  }
});
