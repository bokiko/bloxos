import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

// The pre-hydration bootstrap in layout.tsx is the only code that paints the
// document before React mounts, so it is executed here verbatim rather than
// re-implemented. Monoform replaced the account-scoped design cache this file
// used to cover: there is no layout or palette to restore any more, only the
// theme, and the script must additionally scrub whatever the retired
// multi-theme / multi-layout system left on <html>.
//
// The product has a dark theme and a light one, `dark` being the default. The
// `dark` class is what Tailwind's `dark:` variants key off, so the tests below
// pin BOTH directions — present in dark, absent in light, with `color-scheme`
// following — because a light document that keeps the class renders with dark
// form controls and dark shadcn focus surfaces.

function bootstrapSource() {
  const layout = readFileSync(new URL('../app/layout.tsx', import.meta.url), 'utf8');
  const match = layout.match(/const appearanceBootstrapScript = `([\s\S]*?)`\.trim\(\);/);
  assert.ok(match, 'layout.tsx must define appearanceBootstrapScript');
  return match[1];
}

/** A <html> stand-in: a real array for classList so Array.prototype.slice works. */
function makeRoot(classes = [], dataset = {}) {
  const classList = [...classes];
  classList.add = (c) => { if (!classList.includes(c)) classList.push(c); };
  classList.remove = (c) => { const i = classList.indexOf(c); if (i >= 0) classList.splice(i, 1); };
  return { dataset: { ...dataset }, style: {}, classList };
}

function run(root, storage) {
  const getItem = typeof storage === 'function'
    ? storage
    : (key) => (key in storage ? storage[key] : null);
  runInNewContext(bootstrapSource(), { localStorage: { getItem }, document: { documentElement: root } });
  return root;
}

test('the bootstrap paints the stored theme, and only dark or light', () => {
  for (const stored of ['dark', 'light']) {
    assert.equal(run(makeRoot(), { 'bloxos-appearance': stored }).dataset.appearance, stored);
  }
  // Anything that is not one of the two themes must resolve to the default and
  // never be applied. `gray` is in this list on purpose: it was a real stored
  // value one release ago, so every browser that used the previous build has
  // it, and it must migrate to dark rather than paint an unstyled document.
  for (const junk of ['gray', 'system', 'dracula', 'tokyo-night', '', 'null']) {
    const root = run(makeRoot(), { 'bloxos-appearance': junk });
    assert.equal(root.dataset.appearance, 'dark', `${junk} must not survive as an appearance`);
  }
});

test('the retired contrast-mode key cannot select a theme', () => {
  // `bloxos-theme-mode` belonged to the pre-Monoform theme gallery. It is no
  // longer read at all: with dark as the default, the only value it could
  // still rescue is the one an absent key already produces, and honouring its
  // "light" would flip a user who has been on a dark surface for a full
  // release into a different light theme they never chose.
  for (const legacy of ['dark', 'light', 'system']) {
    assert.equal(run(makeRoot(), { 'bloxos-theme-mode': legacy }).dataset.appearance, 'dark');
  }
  // ...and it never overrides a current choice.
  const current = run(makeRoot(), { 'bloxos-appearance': 'light', 'bloxos-theme-mode': 'dark' });
  assert.equal(current.dataset.appearance, 'light');
});

test('the dark class and color-scheme follow the theme in both directions', () => {
  const dark = run(makeRoot(), { 'bloxos-appearance': 'dark' });
  assert.ok(dark.classList.includes('dark'), 'dark must carry the dark class');
  assert.equal(dark.style.colorScheme, 'dark');

  // Starting from a document that already carries the class — the common case,
  // since the previous theme was dark — light must take it off again.
  const light = run(makeRoot(['dark']), { 'bloxos-appearance': 'light' });
  assert.ok(!light.classList.includes('dark'), 'light must not carry the dark class');
  assert.equal(light.style.colorScheme, 'light');
});

test('the bootstrap scrubs what the retired layout and theme system left behind', () => {
  const root = run(
    makeRoot(['dark', 'theme-dracula', 'theme-graphite', 'keep-me'], { layout: 'grove', designColor: 'bright' }),
    { 'bloxos-appearance': 'dark' },
  );
  assert.deepEqual(
    [...root.classList].filter((c) => c.startsWith('theme-')),
    [],
    'no retired palette class may survive',
  );
  assert.ok(root.classList.includes('keep-me'), 'unrelated classes must be left alone');
  assert.ok(!('layout' in root.dataset), 'the retired data-layout attribute must be cleared');
  assert.ok(!('designColor' in root.dataset), 'the retired data-design-color attribute must be cleared');
});

test('unavailable storage still yields a painted, readable document', () => {
  const root = makeRoot();
  run(root, () => { throw new Error('storage blocked'); });
  assert.equal(root.dataset.appearance, 'dark');
  assert.ok(root.classList.includes('dark'));
});
