import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { buildInfo } from './build-info.mjs';

test('build identity is minimal and process-stable', () => {
  const a = buildInfo('v1.2.3', 'abc123');
  assert.deepEqual(a, buildInfo('v1.2.3', 'abc123'));
  assert.deepEqual(Object.keys(a).sort(), ['component', 'instance_id', 'revision', 'version']);
  assert.equal(a.component, 'dashboard');
  assert.equal(a.version, 'v1.2.3');
  assert.equal(a.revision, 'abc123');
  assert.match(a.instance_id, /^[0-9a-f-]{36}$/);
  assert.equal(buildInfo().revision, 'unknown');
});

test('a different process has a different identity even on the same build', () => {
  const moduleURL = new URL('./build-info.mjs', import.meta.url).href;
  const child = spawnSync(process.execPath, ['--input-type=module', '-e',
    `import {buildInfo} from ${JSON.stringify(moduleURL)}; process.stdout.write(buildInfo().instance_id)`], {encoding:'utf8'});
  assert.equal(child.status, 0, child.stderr);
  assert.notEqual(child.stdout, buildInfo().instance_id);
});

test('route bypasses static caching and build metadata is compiled in', () => {
  const route = readFileSync(new URL('../app/build-info/route.ts', import.meta.url), 'utf8');
  const config = readFileSync(new URL('../../next.config.ts', import.meta.url), 'utf8');
  assert.match(route, /force-dynamic/);
  assert.match(route, /no-store/);
  assert.match(config, /BLOXOS_BUILD_VERSION: process.env.BLOXOS_BUILD_VERSION/);
  assert.match(config, /BLOXOS_BUILD_REVISION: process.env.BLOXOS_BUILD_REVISION/);
});
