import assert from "node:assert/strict";
import test from "node:test";
import { installTokenMinutesRemaining } from "./install-token-expiry.ts";

test("install countdown never exceeds the 15-minute lifetime", () => {
  for (const remainingMs of [900_000, 900_001, 930_000, 3_600_000]) {
    assert.equal(installTokenMinutesRemaining(remainingMs), 15);
  }
});

test("install countdown preserves rounding and expiration boundaries", () => {
  for (const [remainingMs, minutes] of [
    [840_000, 14], [60_001, 2], [60_000, 1], [1, 1], [0, 0], [-1, 0],
  ]) {
    assert.equal(installTokenMinutesRemaining(remainingMs), minutes);
  }
});
