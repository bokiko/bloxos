import test from 'node:test';
import assert from 'node:assert/strict';
import { DESIGN_LAYOUTS, DESIGN_COLORS, normalizeDesign, readDesign, writeDesign, hasDesignCache } from './design-prefs.mjs';

test('designs and colors are independent and have exactly nine new combinations', () => {
  assert.equal((DESIGN_LAYOUTS.length - 1) * DESIGN_COLORS.length, 9);
  for (const layout of DESIGN_LAYOUTS) for (const color of DESIGN_COLORS) {
    const prefs = normalizeDesign({ layout, colors: { wall: color, grove: color, console: color } });
    assert.equal(prefs.layout, layout);
    assert.equal(prefs.colors.grove, color);
  }
});
test('only a valid cache for the current account enables immediate rendering', () => {
  const items = new Map();
  const storage = { getItem: key => items.get(key), setItem: (key, value) => items.set(key, value) };
  assert.equal(hasDesignCache(storage, 'one'), false);
  writeDesign(storage, 'one', normalizeDesign({layout: 'grove'}));
  assert.equal(hasDesignCache(storage, 'one'), true);
  assert.equal(hasDesignCache(storage, 'two'), false);
  for (const bad of ['{', 'null', '{"layout":"unknown"}']) {
    items.set('bloxos-design:two', bad);
    assert.equal(hasDesignCache(storage, 'two'), false);
  }
  assert.equal(hasDesignCache({ getItem() { throw Error('denied'); } }, 'one'), false);
});
test('unknown and corrupt preferences fall back to Classic, never a new design', () => {
  for (const value of [null, [], 'wall', { layout: 'verdant', colors: { wall: 'system' } }]) {
    assert.deepEqual(normalizeDesign(value), { layout: 'classic', colors: { wall: 'original', grove: 'original', console: 'original' } });
  }
});
test('cache is account-scoped and unavailable storage is harmless', () => {
  const items = new Map();
  const storage = { getItem: key => items.get(key), setItem: (key, value) => items.set(key, value) };
  const one = normalizeDesign({ layout: 'grove', colors: { grove: 'bright', wall: 'dark' } });
  writeDesign(storage, 'one', one);
  assert.deepEqual(readDesign(storage, 'one'), one);
  assert.equal(readDesign(storage, 'two').layout, 'classic');
  assert.equal(readDesign(storage, null).layout, 'classic');
  const blocked = { getItem() { throw Error('denied'); }, setItem() { throw Error('full'); } };
  assert.equal(readDesign(blocked, 'one').layout, 'classic');
  assert.doesNotThrow(() => writeDesign(blocked, 'one', one));
});
