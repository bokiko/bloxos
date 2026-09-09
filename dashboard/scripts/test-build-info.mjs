// Run after a production build, with EXPECT_VERSION/EXPECT_REVISION set to
// its build stamps. Exercise the shipped standalone server, not a mock route.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';

const { EXPECT_VERSION, EXPECT_REVISION } = process.env;
assert.ok(EXPECT_VERSION && EXPECT_REVISION, 'expected build stamps are required');

async function checkProcess() {
  const reservation = createServer();
  reservation.listen(0, '127.0.0.1');
  await once(reservation, 'listening');
  const port = reservation.address().port;
  await new Promise(resolve => reservation.close(resolve));
  const child = spawn(process.execPath, ['.next/standalone/server.js'], {
    env: { ...process.env, HOSTNAME: '127.0.0.1', PORT: String(port),
      BLOXOS_BUILD_VERSION: 'wrong-runtime-version',
      BLOXOS_BUILD_REVISION: 'wrong-runtime-revision' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let logs = '';
  child.stdout.on('data', data => { logs = (logs + data).slice(-4000); });
  child.stderr.on('data', data => { logs = (logs + data).slice(-4000); });
  let spawnError;
  child.on('error', error => { spawnError = error; });
  const read = () => fetch(`http://127.0.0.1:${port}/build-info`,
    { signal: AbortSignal.timeout(2000), redirect: 'error' });
  try {
    let response;
    for (let attempt = 0; attempt < 50; attempt++) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null) throw new Error(`server exited: ${logs}`);
      try { response = await read(); break; } catch { await delay(100); }
    }
    assert.ok(response?.ok, `standalone server did not become ready: ${logs}`);
    assert.match(response.headers.get('cache-control'), /no-store/);
    const first = await response.json();
    assert.equal(first.component, 'dashboard');
    assert.equal(first.version, EXPECT_VERSION);
    assert.equal(first.revision, EXPECT_REVISION);
    assert.match(first.instance_id, /^[0-9a-f-]{36}$/);
    assert.deepEqual(await (await read()).json(), first);
    return first.instance_id;
  } finally {
    if (child.exitCode === null && child.pid) {
      const stopped = once(child, 'exit');
      child.kill('SIGTERM');
      const timeout = setTimeout(() => child.kill('SIGKILL'), 2000);
      await stopped;
      clearTimeout(timeout);
    }
  }
}

const first = await checkProcess();
const second = await checkProcess();
assert.notEqual(first, second, 'different dashboard processes must have different identities');
console.log('PASS: standalone build stamps survive runtime overrides; instance identity changes on restart');
