// Every layout must be WIRED, not merely declared.
//
// A layout can typecheck, lint, build and pass every unit test while rendering
// a completely blank page, because the two places that must know about it are
// plain runtime string comparisons:
//
//   * AppShell dispatches children into per-layout chrome. A layout missing
//     from that dispatch mounts no children at all — an empty shell.
//   * The pre-hydration bootstrap in app/layout.tsx whitelists the layouts it
//     will paint before React runs. A layout missing there flashes the wrong
//     chrome and colour scheme on every load.
//
// Both were missed for Ledger and found in review, not by the build. These
// assertions read the sources so adding a sixth layout cannot repeat it.

import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { DESIGN_LAYOUTS } from './design-prefs.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const read = (rel) => readFileSync(join(here, rel), 'utf8');

// Classic is a deliberate passthrough: AppShell returns children directly and
// the bootstrap leaves the user's own theme alone.
const LIVE_LAYOUTS = DESIGN_LAYOUTS.filter((layout) => layout !== 'classic');

test('AppShell mounts children for every live layout', () => {
  const source = read('../components/shell/AppShell.tsx');
  assert.match(source, /layout === "classic"/,
    'classic must stay an explicit passthrough');
  for (const layout of LIVE_LAYOUTS) {
    assert.ok(source.includes(`layout === "${layout}"`),
      `AppShell has no chrome branch for "${layout}" — it would render an empty shell`);
  }
});

test('the pre-hydration bootstrap paints every live layout', () => {
  const source = read('../app/layout.tsx');
  const match = source.match(/\[((?:'[a-z]+',?)+)\]\.indexOf\(saved\.layout\)/);
  assert.ok(match, 'could not find the bootstrap layout whitelist in app/layout.tsx');
  const whitelisted = match[1].split(',').map((name) => name.trim().replace(/'/g, ''));
  assert.deepEqual(whitelisted.slice().sort(), LIVE_LAYOUTS.slice().sort(),
    'the bootstrap whitelist and DESIGN_LAYOUTS disagree; a missing layout flashes the wrong chrome');
});

test('every live layout is offered in appearance settings', () => {
  const source = read('../components/DesignSettings.tsx');
  for (const layout of DESIGN_LAYOUTS) {
    assert.ok(source.includes(`id: "${layout}"`),
      `DesignSettings does not offer "${layout}", so it cannot be selected`);
  }
});
