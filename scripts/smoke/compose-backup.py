#!/usr/bin/env python3
"""Restore the disposable enrollment stack, with no host ports or network.

Run after compose-enroll has initialized its stack, before cleanup. Only the
fresh project created here is removed; the source stack and backup are kept.
"""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time
import uuid


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def output(*args):
    return subprocess.check_output(args)


def contents(data):
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        return {m.name.removeprefix('./'): (m.uid, m.gid, m.mode, hashlib.sha256(archive.extractfile(m).read()).hexdigest())
                for m in archive if m.isfile()}


def main():
    if os.environ.get('SMOKE_CONFIRM_DISPOSABLE') != '1':
        raise SystemExit('Requires SMOKE_CONFIRM_DISPOSABLE=1; source must be the disposable enrollment stack.')
    root = Path(__file__).resolve().parents[2]
    source = ['docker', 'compose', '--project-directory', str(root / 'docker')]
    ids = {service: output(*source, 'ps', '-a', '-q', service).decode().strip() for service in ('hub', 'caddy')}
    assert all(ids.values()), 'source stack missing'
    specs = {service: json.loads(output('docker', 'inspect', container))[0] for service, container in ids.items()}
    # Retain backups for failure diagnosis; they contain test-only secrets.
    scratch = Path(tempfile.mkdtemp(prefix='bloxos-backup-smoke-'))
    scratch.chmod(0o700)
    backup = scratch / 'backup'
    run('bash', str(root / 'scripts/backup-compose.sh'), str(backup), '--project-directory', str(root / 'docker'))
    name = 'bloxos-restore-' + uuid.uuid4().hex[:12]
    config = {
        'name': name,
        'services': {
            'hub': {'image': specs['hub']['Image'], 'network_mode': 'none',
                    'environment': specs['hub']['Config']['Env'],
                    'volumes': ['hub-data:/data', 'caddy-data:/caddy:ro']},
            'caddy': {'image': specs['caddy']['Image'], 'network_mode': 'none',
                      'volumes': ['caddy-data:/data', 'caddy-config:/config']},
        },
        'volumes': {'hub-data': {}, 'caddy-data': {}, 'caddy-config': {}},
    }
    path = scratch / 'restore.json'
    path.touch(mode=0o600)
    path.write_text(json.dumps(config))
    restore = ['docker', 'compose', '-p', name, '-f', str(path)]
    try:
        run(*restore, 'create', '--no-build')
        targets = {service: output(*restore, 'ps', '-a', '-q', service).decode().strip() for service in ids}
        for service, directory, archive in [('hub', '/data', 'hub-data'), ('caddy', '/data', 'caddy-data'), ('caddy', '/config', 'caddy-config')]:
            target = targets[service] + ':' + directory
            data = (backup / (archive + '.tar')).read_bytes()
            assert not contents(output('docker', 'cp', target + '/.', '-')), 'target unexpectedly populated'
            run('docker', 'cp', '-a', '-', target, input=data)
            assert contents(data) == contents(output('docker', 'cp', target + '/.', '-')), 'restored bytes/ownership differ'
        # Start only the restored hub. network_mode:none prevents a cloned
        # poller/identity from reaching any real device, even on a reused VM.
        run('docker', 'start', targets['hub'])
        for attempt in range(30):
            probe = subprocess.run(['docker', 'exec', targets['hub'], 'wget', '-qO-', 'http://127.0.0.1:4000/health'], capture_output=True)
            if probe.returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError('restored hub did not become healthy')
        status = json.loads(output('docker', 'exec', targets['hub'], 'wget', '-qO-', 'http://127.0.0.1:4000/api/setup/status'))
        assert status['needs_setup'] is False, 'restored hub lost its existing admin'
        print('BACKUP RESTORE PASS: all archive bytes, ownership, identity and initialized hub survive restore; backup:', backup)
    finally:
        # Exact unique project above, never the source project. No external
        # volumes exist in this generated config. Backup is kept for diagnosis.
        run(*restore, 'down', '-v')


if __name__ == '__main__':
    main()
