import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

// The pre-hydration bootstrap in layout.tsx is the only code that paints the
// document before React mounts, so it is executed here verbatim rather than
// re-implemented. Monoform replaced the account-scoped design cache this file
// used to cover: there is no layout or palette to restore any more, only the
// contrast mode, and the script must additionally scrub whatever the retired
// multi-theme / multi-layout system left on <html>.

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

test('the bootstrap paints the stored contrast mode, and only gray or dark', () => {
  for (const stored of ['dark', 'gray']) {
    assert.equal(run(makeRoot(), { 'bloxos-appearance': stored }).dataset.appearance, stored);
  }
  // Anything that is not one of the two modes — a retired theme name, a
  // retired mode, junk — must resolve to the default, never be applied.
  for (const junk of ['light', 'system', 'dracula', 'tokyo-night', '', 'null']) {
    const root = run(makeRoot(), { 'bloxos-appearance': junk });
    assert.equal(root.dataset.appearance, 'gray', `${junk} must not survive as an appearance`);
  }
});

test('an existing dark choice survives the reset through the legacy mode key', () => {
  assert.equal(run(makeRoot(), { 'bloxos-theme-mode': 'dark' }).dataset.appearance, 'dark');
  // The legacy key is read one way only: a legacy light/system choice does not
  // resurrect a light page, because Monoform has no light mode.
  for (const legacy of ['light', 'system']) {
    assert.equal(run(makeRoot(), { 'bloxos-theme-mode': legacy }).dataset.appearance, 'gray');
  }
  // A current choice always wins over the legacy key.
  const current = run(makeRoot(), { 'bloxos-appearance': 'gray', 'bloxos-theme-mode': 'dark' });
  assert.equal(current.dataset.appearance, 'gray');
});

test('both modes paint a dark document, so the first frame is never light', () => {
  for (const stored of ['gray', 'dark']) {
    const root = run(makeRoot(), { 'bloxos-appearance': stored });
    assert.ok(root.classList.includes('dark'), `${stored} must keep the dark class`);
    assert.equal(root.style.colorScheme, 'dark');
  }
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
  assert.equal(root.dataset.appearance, 'gray');
  assert.ok(root.classList.includes('dark'));
});
