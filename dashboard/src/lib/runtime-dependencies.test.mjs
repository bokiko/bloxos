import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

test('React and React DOM use matching versions, including error-page rendering', () => {
  const { dependencies } = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8'));
  assert.equal(dependencies.react, dependencies['react-dom']);
});
