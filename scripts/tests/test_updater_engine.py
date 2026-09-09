#!/usr/bin/env python3
"""Offline tests for the update engine: manifest/artifact safety, the mailbox
and request boundary, secure config, the phase machine, rollback, bounded
readiness verification, and interruption recovery. No network, no subprocess."""
import io
import json
import os
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from updater import engine  # noqa: E402


def good_manifest():
    return {
        "schema": 1, "version": "v1.2.3", "revision": "a" * 40,
        "images": {"hub": "ghcr.io/x@sha256:" + "b" * 64},
        "native": {"amd64": {"file": "bloxos-server-linux-amd64.tar.gz", "sha256": "c" * 64}},
    }


def make_config(tmp, **over):
    d = {"mode": "native", "public_url": "https://hub.example",
         "mailbox_dir": os.path.join(tmp, "mailbox"),
         "state_dir": os.path.join(tmp, "state")}
    d.update(over)
    return engine.Config.from_dict(d)


class FakeAdapter:
    """Records the method sequence; can fail at one named method."""

    def __init__(self, identities=None, fail_at=None):
        self.calls = []
        self._identities = identities or {}
        self.fail_at = {fail_at} if isinstance(fail_at, str) else set(fail_at or [])

    def _c(self, name):
        self.calls.append(name)
        if name in self.fail_at:
            raise engine.UpdaterError(f"boom-{name}")

    def preflight(self): self._c("preflight")
    def stage(self, m, r): self._c("stage")
    def quiesce(self): self._c("quiesce")
    def backup(self): self._c("backup")
    def install(self): self._c("install")
    def start_candidate(self): self._c("start_candidate")
    def identities(self): self._c("identities"); return self._identities
    def rollback(self): self._c("rollback")
    def resume_original(self): self._c("resume_original")


def matching_identities(version="v1.2.3", revision="a" * 40, instance="inst-1"):
    ident = {"component": "", "version": version, "revision": revision, "instance_id": instance}
    return {"hub": dict(ident, component="hub"), "dashboard": dict(ident, component="dashboard")}


def txn(config, adapter, **kw):
    manifest_bytes = json.dumps(good_manifest()).encode()

    def fetch(url, mx):
        return manifest_bytes

    def identity(base, comp):
        # public identity mirrors the adapter's direct identity by default
        return dict(adapter._identities.get(comp, {}))

    kw.setdefault("fetch_bytes", fetch)
    kw.setdefault("identity_fetch", identity)
    kw.setdefault("sleep", lambda _s: None)
    return engine.Transaction(config, adapter, **kw)


class ManifestTests(unittest.TestCase):
    def test_valid(self):
        m = engine.validate_manifest(json.dumps(good_manifest()).encode())
        self.assertEqual(m["version"], "v1.2.3")
        self.assertIn("amd64", m["native"])

    def test_rejections(self):
        bad = [
            b"not json",
            json.dumps(dict(good_manifest(), schema=2)).encode(),
            json.dumps(dict(good_manifest(), version="1.2.3")).encode(),      # no v
            json.dumps(dict(good_manifest(), revision="z" * 40)).encode(),     # not hex
            json.dumps(dict(good_manifest(), native={})).encode(),            # empty
            json.dumps(dict(good_manifest(),
                            native={"amd64": {"file": "../x.tar.gz", "sha256": "c" * 64}})).encode(),
            json.dumps(dict(good_manifest(),
                            native={"amd64": {"file": "x.zip", "sha256": "c" * 64}})).encode(),
            json.dumps(dict(good_manifest(),
                            native={"amd64": {"file": "x.tar.gz", "sha256": "nothex"}})).encode(),
            json.dumps(dict(good_manifest(),
                            native={"riscv": {"file": "x.tar.gz", "sha256": "c" * 64}})).encode(),
        ]
        for raw in bad:
            with self.assertRaises(engine.UpdaterError):
                engine.validate_manifest(raw)


class ArchiveSafetyTests(unittest.TestCase):
    def _tar(self, tmp, add):
        path = os.path.join(tmp, "a.tar.gz")
        with tarfile.open(path, "w:gz") as tar:
            add(tar)
        return path

    def test_clean_extract(self):
        with tempfile.TemporaryDirectory() as tmp:
            def add(tar):
                data = b"hello"
                info = tarfile.TarInfo("dir/file.txt")
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
            path = self._tar(tmp, add)
            dest = os.path.join(tmp, "out")
            engine.safe_extract_tar(path, dest)
            self.assertTrue(os.path.isfile(os.path.join(dest, "dir", "file.txt")))

    def test_traversal_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            def add(tar):
                info = tarfile.TarInfo("../escape.txt")
                info.size = 0
                tar.addfile(info, io.BytesIO(b""))
            path = self._tar(tmp, add)
            with self.assertRaises(engine.UpdaterError):
                engine.safe_extract_tar(path, os.path.join(tmp, "out"))

    def test_absolute_symlink_rejected(self):
        # safe_extract_tar ownership moved to main (internal-relative-symlink
        # policy + scripts/tests/test_updater_extract.py). An ABSOLUTE symlink
        # target must still be rejected under any policy.
        with tempfile.TemporaryDirectory() as tmp:
            def add(tar):
                info = tarfile.TarInfo("link")
                info.type = tarfile.SYMTYPE
                info.linkname = "/etc/passwd"
                tar.addfile(info)
            path = self._tar(tmp, add)
            with self.assertRaises(engine.UpdaterError):
                engine.safe_extract_tar(path, os.path.join(tmp, "out"))


class DownloadTests(unittest.TestCase):
    def test_sha_mismatch_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(engine.UpdaterError):
                engine.download_and_verify("https://github.com/x", os.path.join(tmp, "f"),
                                           "a" * 64, lambda url, mx: b"payload")

    def test_sha_match_writes(self):
        import hashlib
        payload = b"payload"
        with tempfile.TemporaryDirectory() as tmp:
            dest = os.path.join(tmp, "f")
            engine.download_and_verify("https://github.com/x", dest,
                                       hashlib.sha256(payload).hexdigest(),
                                       lambda url, mx: payload)
            self.assertEqual(open(dest, "rb").read(), payload)


class MailboxTests(unittest.TestCase):
    def test_request_roundtrip_and_strictness(self):
        with tempfile.TemporaryDirectory() as tmp:
            mb = engine.Mailbox(os.path.join(tmp, "mb"))
            mb.ensure()
            self.assertIsNone(mb.read_request())
            engine._atomic_write_json(mb.request_path,
                                      {"request_id": "550e8400-e29b-41d4-a716-446655440000",
                                       "target_version": "latest"})
            req = mb.read_request()
            self.assertEqual(req["target_version"], "latest")
            # extra field rejected, not ignored
            engine._atomic_write_json(mb.request_path,
                                      {"request_id": "550e8400-e29b-41d4-a716-446655440000",
                                       "target_version": "latest", "cmd": "rm -rf"})
            with self.assertRaises(engine.UpdaterError):
                mb.read_request()

    def test_request_symlink_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            mb = engine.Mailbox(os.path.join(tmp, "mb"))
            mb.ensure()
            target = os.path.join(tmp, "real.json")
            open(target, "w").write("{}")
            os.symlink(target, mb.request_path)
            with self.assertRaises(engine.UpdaterError):
                mb.read_request()

    def test_outbox_is_world_readable(self):
        with tempfile.TemporaryDirectory() as tmp:
            mb = engine.Mailbox(os.path.join(tmp, "mb"))
            mb.ensure()
            mb.write_status("idle")
            mb.set_maintenance()
            self.assertEqual(os.stat(mb.status_path).st_mode & 0o777, 0o644)
            self.assertEqual(os.stat(mb.maintenance_path).st_mode & 0o777, 0o644)
            self.assertTrue(mb.maintenance_present())
            mb.clear_maintenance()
            self.assertFalse(mb.maintenance_present())

    def test_maintenance_directory_changes_are_durable(self):
        with tempfile.TemporaryDirectory() as tmp:
            mb = engine.Mailbox(os.path.join(tmp, "mb"))
            mb.ensure()
            with patch.object(engine, "_fsync_parent", wraps=engine._fsync_parent) as sync:
                mb.set_maintenance()
                sync.assert_called_once_with(mb.maintenance_path)
                sync.reset_mock()
                mb.clear_maintenance()
                sync.assert_called_once_with(mb.maintenance_path)

    def test_maintenance_fsync_failure_is_not_silently_accepted(self):
        with tempfile.TemporaryDirectory() as tmp:
            mb = engine.Mailbox(os.path.join(tmp, "mb"))
            mb.ensure()
            with patch.object(engine, "_fsync_parent", side_effect=engine.UpdaterError("disk failure")):
                with self.assertRaises(engine.UpdaterError):
                    mb.set_maintenance()
                with self.assertRaises(engine.UpdaterError):
                    mb.clear_maintenance()


class ConfigSecurityTests(unittest.TestCase):
    def test_group_writable_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "config.json")
            with open(path, "w") as fh:
                json.dump({"mode": "native", "public_url": "https://h",
                           "mailbox_dir": tmp, "state_dir": tmp}, fh)
            os.chmod(path, 0o664)  # group-writable
            with self.assertRaises(engine.UpdaterError):
                engine.Config.load(path)
            # relaxed load accepts it (used by tests/non-root contexts)
            cfg = engine.Config.load(path, require_secure=False)
            self.assertEqual(cfg.mode, "native")

    def test_symlink_config_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            real = os.path.join(tmp, "real.json")
            with open(real, "w") as fh:
                json.dump({"mode": "native", "public_url": "https://h",
                           "mailbox_dir": tmp, "state_dir": tmp}, fh)
            os.chmod(real, 0o600)
            link = os.path.join(tmp, "config.json")
            os.symlink(real, link)
            with self.assertRaises(engine.UpdaterError):
                engine.Config.load(link, require_secure=False)


class LockTests(unittest.TestCase):
    def test_exclusive(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "l")
            with engine.exclusive_lock(path):
                with self.assertRaises(engine.UpdaterError):
                    with engine.exclusive_lock(path):
                        pass


class TransactionTests(unittest.TestCase):
    def test_reopen_happens_only_after_durable_commit_and_retries_without_restore(self):
        for failure, expected in ((None, engine.SUCCEEDED), ("install", engine.ROLLED_BACK), ("backup", engine.FAILED)):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                cfg = make_config(tmp)
                adapter = FakeAdapter(matching_identities(), fail_at=failure)
                transaction = txn(cfg, adapter)
                observed = []
                def finalize():
                    journal = engine.Journal(transaction.transaction_dir)
                    self.assertEqual(journal.phase(), expected)
                    observed.append(journal.phase())
                    if len(observed) == 1:
                        raise engine.UpdaterError("proxy temporarily unavailable")
                adapter.finalize = finalize
                self.assertEqual(transaction.apply("550e8400-e29b-41d4-a716-446655440000"), engine.FAILED)
                self.assertTrue(transaction.journal.exists())
                calls_before = list(adapter.calls)
                self.assertEqual(transaction.recover(), expected)
                self.assertEqual(adapter.calls, calls_before, "terminal recovery must not restore or restart the old snapshot")
                self.assertEqual(observed, [expected, expected])
                self.assertFalse(transaction.journal.exists())

    def test_happy_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.SUCCEEDED)
            self.assertEqual(adapter.calls,
                             ["preflight", "stage", "quiesce", "backup", "install",
                              "start_candidate", "identities"])
            mb = engine.Mailbox(cfg.mailbox_dir)
            self.assertFalse(mb.maintenance_present())
            self.assertFalse(engine.Journal(os.path.join(cfg.state_dir, "transaction")).exists())

    def test_predowntime_failure_is_clean(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities(), fail_at="stage")
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.FAILED)
            self.assertNotIn("install", adapter.calls)  # nothing switched
            self.assertNotIn("rollback", adapter.calls)

    def test_backup_failure_resumes_original(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities(), fail_at="backup")
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.FAILED)
            self.assertIn("resume_original", adapter.calls)
            self.assertNotIn("install", adapter.calls)

    def test_backup_and_resume_both_fail_keep_journal(self):
        # The worst pre-install case: backup fails AND the originals cannot be
        # restarted. The journal and request MUST be retained so a later worker
        # run can retry recovery — never silently cleared.
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            engine.write_update_request(cfg)
            adapter = FakeAdapter(matching_identities(), fail_at=["backup", "resume_original"])
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.FAILED)
            self.assertTrue(engine.Journal(os.path.join(cfg.state_dir, "transaction")).exists())
            self.assertTrue(os.path.exists(engine.Mailbox(cfg.mailbox_dir).request_path))
            self.assertIn("manual recovery required", engine.read_status(cfg)["message"])

    def test_verify_mismatch_rolls_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            # direct identities report a different build than the manifest
            adapter = FakeAdapter(matching_identities(version="v0.0.1"))
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.ROLLED_BACK)
            self.assertIn("rollback", adapter.calls)
            self.assertFalse(engine.Mailbox(cfg.mailbox_dir).maintenance_present())

    def test_public_instance_mismatch_retries_then_rolls_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities(instance="local-1"))

            def public_diff(base, comp):
                d = dict(adapter._identities[comp]); d["instance_id"] = "other"; return d
            t = txn(cfg, adapter, identity_fetch=public_diff, readiness_timeout=0.05)
            state = t.apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.ROLLED_BACK)

    def test_readiness_retry_succeeds(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities())
            attempts = {"n": 0}

            def flaky_public(base, comp):
                attempts["n"] += 1
                if attempts["n"] < 4:
                    raise engine.UpdaterError("not ready")
                return dict(adapter._identities[comp])
            t = txn(cfg, adapter, identity_fetch=flaky_public,
                    readiness_timeout=100.0, poll_interval=0.0)
            state = t.apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.SUCCEEDED)
            self.assertGreaterEqual(attempts["n"], 4)

    def test_rollback_failure_is_loud(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            adapter = FakeAdapter(matching_identities(version="v0.0.1"), fail_at="rollback")
            state = txn(cfg, adapter).apply("550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(state, engine.FAILED)
            status = engine.read_status(cfg)
            self.assertIn("manual recovery required", status["message"])
            # journal retained for recovery
            self.assertTrue(engine.Journal(os.path.join(cfg.state_dir, "transaction")).exists())


class RecoveryTests(unittest.TestCase):
    def _seed_journal(self, cfg, phase):
        tdir = os.path.join(cfg.state_dir, "transaction")
        os.makedirs(tdir, exist_ok=True)
        engine.Journal(tdir).set_phase(phase)

    def test_recover_after_install_rolls_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            self._seed_journal(cfg, engine.INSTALLING)
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.ROLLED_BACK)
            self.assertIn("rollback", adapter.calls)

    def test_recover_before_install_resumes(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            self._seed_journal(cfg, engine.STAGING)
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.FAILED)
            self.assertIn("resume_original", adapter.calls)
            self.assertNotIn("rollback", adapter.calls)

    def test_recover_mutating_preserves_rid_and_version(self):
        # A terminal status written by recovery must carry the request_id and
        # version from the journal, so CLI/hub polling sees the outcome after a
        # restart instead of a blank record.
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            tdir = os.path.join(cfg.state_dir, "transaction")
            os.makedirs(tdir, exist_ok=True)
            j = engine.Journal(tdir)
            j.set_phase(engine.INSTALLING)
            j.record("request_id", "550e8400-e29b-41d4-a716-446655440000")
            j.record("version", "v1.2.3")
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.ROLLED_BACK)
            status = engine.read_status(cfg)
            self.assertEqual(status["request_id"], "550e8400-e29b-41d4-a716-446655440000")
            self.assertEqual(status["version"], "v1.2.3")

    def test_recover_rollback_failure_retains_journal(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            self._seed_journal(cfg, engine.INSTALLING)
            adapter = FakeAdapter(matching_identities(), fail_at="rollback")
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.FAILED)
            self.assertTrue(engine.Journal(os.path.join(cfg.state_dir, "transaction")).exists())
            self.assertIn("manual recovery required", engine.read_status(cfg)["message"])

    def test_recover_committed_boundary_finalizes_no_rollback(self):
        # Crash AFTER traffic resumed but BEFORE the journal was cleared: the
        # durable SUCCEEDED phase must finalize, never roll back accepted writes.
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            self._seed_journal(cfg, engine.SUCCEEDED)
            engine.Mailbox(cfg.mailbox_dir).set_maintenance()
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.SUCCEEDED)
            self.assertNotIn("rollback", adapter.calls)
            self.assertFalse(engine.Mailbox(cfg.mailbox_dir).maintenance_present())
            self.assertEqual(engine.read_status(cfg)["state"], engine.SUCCEEDED)

    def test_recover_rolledback_boundary_finalizes_no_rollback(self):
        # Crash after rollback completed but before the marker cleared: finalize
        # the completed rollback, never run rollback again.
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            self._seed_journal(cfg, engine.ROLLED_BACK)
            engine.Mailbox(cfg.mailbox_dir).set_maintenance()
            adapter = FakeAdapter(matching_identities())
            state = txn(cfg, adapter).recover()
            self.assertEqual(state, engine.ROLLED_BACK)
            self.assertNotIn("rollback", adapter.calls)
            self.assertFalse(engine.Mailbox(cfg.mailbox_dir).maintenance_present())


class WorkerAndArchiveTests(unittest.TestCase):
    def test_archive_retains_all(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = make_config(tmp)
            tdir = os.path.join(cfg.state_dir, "transaction")
            os.makedirs(tdir)
            open(os.path.join(tdir, "marker1"), "w").write("1")
            first = engine.archive_completed_transaction(cfg.state_dir)
            os.makedirs(tdir)
            open(os.path.join(tdir, "marker2"), "w").write("2")
            import time as _t
            _t.sleep(1)  # ensure a distinct UTC-second name
            second = engine.archive_completed_transaction(cfg.state_dir)
            self.assertTrue(os.path.isdir(first) and os.path.isdir(second))
            self.assertNotEqual(first, second)
            backups = [d for d in os.listdir(cfg.state_dir) if d.startswith("backup-")]
            self.assertEqual(len(backups), 2)

    def test_run_worker_idle_and_apply(self):
        # run_worker builds its own Transaction with the module default fetch /
        # identity; patch those to stay offline.
        ident = matching_identities()
        orig_fetch, orig_id = engine.default_fetch_bytes, engine.default_identity_fetch
        engine.default_fetch_bytes = lambda url, mx, *a, **k: json.dumps(good_manifest()).encode()
        engine.default_identity_fetch = lambda base, comp, *a, **k: dict(ident[comp])
        try:
            with tempfile.TemporaryDirectory() as tmp:
                cfg = make_config(tmp)
                adapter = FakeAdapter(ident)
                self.assertEqual(engine.run_worker(cfg, adapter), engine.IDLE)
                engine.write_update_request(cfg)
                state = engine.run_worker(cfg, adapter)
                self.assertEqual(state, engine.SUCCEEDED)
        finally:
            engine.default_fetch_bytes, engine.default_identity_fetch = orig_fetch, orig_id


if __name__ == "__main__":
    unittest.main()
