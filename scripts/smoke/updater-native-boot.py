#!/usr/bin/env python3
"""Interrupted-update reboot test for the DISPOSABLE native smoke VM only.

First initialize the host updater on the fixture made by updater-native.py.
Run `prepare --candidate-bundle <export-dir>`, reboot that disposable VM, then
run `verify`. No production release-fetch override is exposed by the worker.
The fixture leaves a real VERIFYING journal; the installed boot gate performs
the recovery after reboot, not this script.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import time
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater import engine, native


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def snapshot(config):
    raw = engine.as_config_dict(config)
    root = Path(raw["hub_workdir"]).parent
    database = Path(raw["hub_workdir"]) / "bloxos.db"
    with sqlite3.connect(f"file:{database}?mode=ro", uri=True) as db:
        row = db.execute("SELECT v FROM smoke_fixture WHERE k='seed'").fetchone()
    identity = engine.default_identity_fetch(config.public_url, "hub", config.ca_file, 10)
    dropins = {}
    for field in ("hub_unit", "dashboard_unit"):
        path = Path("/etc/systemd/system") / (raw[field] + ".d") / native.DROPIN_NAME
        dropins[field] = path.read_text() if path.exists() else None
    return {"seed": list(row) if row else None, "owner": [database.stat().st_uid, database.stat().st_gid],
            "ca": hashlib.sha256(Path(config.ca_file).read_bytes()).hexdigest(),
            "source": (root / "operator-source/README.md").read_text(),
            "untracked": (root / "hub-workdir/.kyzn-secret").read_text(),
            "version": identity["version"], "revision": identity["revision"], "dropins": dropins}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("prepare", "verify"))
    parser.add_argument("--candidate-bundle", type=Path)
    args = parser.parse_args()
    if os.environ.get("SMOKE_CONFIRM_DISPOSABLE") != "1" or os.geteuid() != 0:
        raise SystemExit("Requires root and SMOKE_CONFIRM_DISPOSABLE=1 in the disposable fixture")
    config = engine.Config.load("/etc/bloxos-updater/config.json")
    if config.mode != "native" or config.public_url != "https://hub.updater-smoke.test" or not str(config.ca_file).startswith("/tmp/bloxos-updater-native-smoke"):
        raise SystemExit("Not the dedicated native smoke fixture")
    checkpoint = Path(config.state_dir) / "native-boot-smoke.json"
    boot_id = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
    txdir = str(Path(config.state_dir) / "transaction")
    if args.phase == "prepare":
        if not args.candidate_bundle or checkpoint.exists() or engine.Journal(txdir).exists():
            raise SystemExit("Needs candidate bundle, fresh checkpoint, and no unfinished transaction")
        assert command("systemctl", "is-active", "bloxos-updater-recovery.service") == "active"
        saved = snapshot(config)
        engine._atomic_write_json(str(checkpoint), {"boot_id": boot_id, "snapshot": saved})
        manifest = json.loads((args.candidate_bundle / "update-manifest.json").read_text())
        entry = manifest["native"][native.current_arch()]
        payload = (args.candidate_bundle / entry["file"]).read_bytes()
        def fetch(url, maximum):
            if url == engine.RELEASE_LATEST:
                return json.dumps(manifest).encode()
            if url == engine.RELEASE_ASSET.format(version=manifest["version"], file=entry["file"]):
                return payload
            raise AssertionError("Unexpected fixture download")
        class WorkerLoss(BaseException):
            pass
        class Interrupted(native.NativeAdapter):
            def start_candidate(self):
                # Candidate drop-ins are installed, but no candidate traffic has
                # started. Exit without invoking exception-driven rollback.
                raise WorkerLoss()
        with engine.exclusive_lock(str(Path(config.state_dir) / "worker.lock")):
            engine.archive_completed_transaction(config.state_dir)
            adapter = Interrupted(engine.as_config_dict(config), txdir, fetch_bytes=fetch)
            try:
                engine.Transaction(config, adapter, fetch_bytes=fetch).apply(str(uuid.uuid4()))
                raise AssertionError("Did not reach the interruption")
            except WorkerLoss:
                pass
        assert engine.Journal(txdir).phase() == engine.VERIFYING
        assert engine.Mailbox(config.mailbox_dir).maintenance_present()
        print("PREPARED: interrupted VERIFYING journal retained. Reboot ONLY this disposable VM, then run verify.")
        return
    saved = json.loads(checkpoint.read_text())
    assert saved["boot_id"] != boot_id, "A real reboot is required"
    deadline = time.monotonic() + 150
    while True:
        try:
            current = snapshot(config)
            break
        except Exception:
            if time.monotonic() >= deadline:
                raise
            time.sleep(3)
    assert current == saved["snapshot"], "Native data, identity, source, or exact drop-ins changed"
    assert not engine.Journal(txdir).exists(), engine.read_status(config)
    assert not engine.Mailbox(config.mailbox_dir).maintenance_present()
    assert engine.read_status(config)["state"] == engine.ROLLED_BACK
    gate = int(command("systemctl", "show", "bloxos-updater-recovery.service", "-p", "ActiveEnterTimestampMonotonic", "--value"))
    proxy = int(command("systemctl", "show", engine.as_config_dict(config)["proxy_unit"], "-p", "ActiveEnterTimestampMonotonic", "--value"))
    assert gate > 0 and proxy >= gate, "Public proxy started before recovery gate completed"
    print("PASS: real reboot recovered before public proxy started; DB ownership, seed, CA, source and original drop-ins preserved")


if __name__ == "__main__":
    main()
