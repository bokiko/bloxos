"""Fast failure-path tests; real Docker restore is a separate acceptance gate."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


FAKE_DOCKER = r'''#!/usr/bin/env python3
import io, json, os, sys, tarfile
from pathlib import Path
p = Path(os.environ['FAKE_STATE'])
s = json.loads(p.read_text())
a = sys.argv[1:]
s['calls'].append(a)
if a[:1] == ['compose']:
    a = a[1:]
    if a[:1] == ['ps']:
        print(a[-1] + '-id')
    elif a == ['config']:
        print('services: {}')
    elif a[:1] == ['stop']:
        s['running'] = []
elif a[:2] == ['inspect', '-f']:
    print('true' if a[-1] in s['running'] else 'false')
elif a[:1] == ['inspect']:
    print('[]')
elif a[:1] == ['start']:
    s['running'].extend(a[1:])
elif a[:1] == ['cp']:
    if s.get('fail_copy'):
        p.write_text(json.dumps(s))
        sys.exit(42)
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode='w') as archive:
        entry = tarfile.TarInfo('proof')
        entry.size = 4
        archive.addfile(entry, io.BytesIO(b'kept'))
    sys.stdout.buffer.write(output.getvalue())
p.write_text(json.dumps(s))
'''


class BackupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.state = self.root / 'state.json'
        docker = self.root / 'docker'
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o700)
        self.destination = self.root / 'backup'

    def run_backup(self, running=('hub-id', 'caddy-id'), fail_copy=False):
        self.state.write_text(json.dumps({'running': list(running), 'calls': [], 'fail_copy': fail_copy}))
        env = dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH'], FAKE_STATE=str(self.state))
        result = subprocess.run(['bash', str(Path(__file__).with_name('backup-compose.sh')), str(self.destination)],
                                env=env, capture_output=True, text=True)
        return result, json.loads(self.state.read_text())

    def test_complete_backup_is_private_and_restarts_original_containers(self):
        result, state = self.run_backup()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.destination / 'SHA256SUMS').is_file())
        self.assertEqual(self.destination.stat().st_mode & 0o777, 0o700)
        self.assertEqual((self.destination / 'hub-data.tar').stat().st_mode & 0o777, 0o600)
        self.assertEqual(state['running'], ['hub-id', 'caddy-id'])
        stop = state['calls'].index(['compose', 'stop', 'hub', 'caddy'])
        copy = next(i for i, call in enumerate(state['calls']) if call[0] == 'cp')
        self.assertLess(stop, copy)
        self.assertIn(['start', 'hub-id', 'caddy-id'], state['calls'])

    def test_copy_failure_restores_service_state_and_has_no_completion_manifest(self):
        result, state = self.run_backup(fail_copy=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.destination / 'SHA256SUMS').exists())
        self.assertEqual(state['running'], ['hub-id', 'caddy-id'])

    def test_stopped_services_stay_stopped(self):
        result, state = self.run_backup(running=())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['running'], [])
        self.assertFalse(any(call[0] == 'start' for call in state['calls']))

    def test_no_dependency_start_when_only_caddy_was_running(self):
        result, state = self.run_backup(running=('caddy-id',))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(state['running'], ['caddy-id'])

    def test_existing_destination_is_untouched(self):
        self.destination.mkdir()
        marker = self.destination / 'keep'
        marker.write_text('original')
        result, state = self.run_backup()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), 'original')
        self.assertEqual(state['calls'], [])


if __name__ == '__main__':
    unittest.main()
