"""Boot-recovery gate (PR#217): journal-only recovery that fails closed and,
on native, hands the proxy start to systemd as a non-blocking enqueue so the
gate that is ordered BEFORE the proxy never self-waits."""
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater import boot, engine, native


def _config(state_dir, mode="native"):
    return {
        "mode": mode, "state_dir": state_dir,
        "mailbox_dir": os.path.join(state_dir, "mailbox"),
        "public_url": "https://hub.example", "ca_file": None,
        "proxy_unit": "caddy.service", "hub_unit": "bloxos-hub.service",
        "dashboard_unit": "bloxos-dashboard.service",
        "hub_url": "http://127.0.0.1:4000", "dashboard_url": "http://127.0.0.1:3000",
        "hub_workdir": state_dir, "hub_home": state_dir, "node_binary": "/bin/true",
    }


def _seed_journal(state_dir):
    txn = os.path.join(state_dir, "transaction")
    os.makedirs(txn, exist_ok=True)
    path = os.path.join(txn, "journal.json")
    open(path, "w").write('{"phase": "installing"}')
    return path


class RecoverBootDecisionTests(unittest.TestCase):
    def test_no_journal_is_a_clean_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(boot.recover_boot(_config(tmp)), 0)

    def test_missing_state_dir_fails_closed(self):
        self.assertEqual(boot.recover_boot({"mode": "native"}), 1)

    def test_terminal_recovery_clears_journal_and_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            journal = _seed_journal(tmp)

            class FakeTxn:  # engine cleared the journal at a durable terminal
                def __init__(self, *a, **k): pass
                def recover(self): os.remove(journal)
            with patch.object(boot, "Transaction", FakeTxn):
                self.assertEqual(boot.recover_boot(_config(tmp)), 0)

    def test_retained_journal_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            _seed_journal(tmp)

            class FakeTxn:  # rollback could not complete -> journal kept
                def __init__(self, *a, **k): pass
                def recover(self): pass
            with patch.object(boot, "Transaction", FakeTxn):
                self.assertEqual(boot.recover_boot(_config(tmp)), 1)

    def test_corrupt_or_unknown_journal_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            _seed_journal(tmp)

            class FakeTxn:  # engine refuses a corrupt/unknown-phase journal
                def __init__(self, *a, **k): pass
                def recover(self): raise engine.UpdaterError("journal is corrupt")
            with patch.object(boot, "Transaction", FakeTxn):
                self.assertEqual(boot.recover_boot(_config(tmp)), 1)


class NativeBootAdapterTests(unittest.TestCase):
    def test_proxy_start_becomes_nonblocking_backends_stay_real(self):
        recorded = []

        def fake_default_runner(cmd):
            recorded.append(cmd)
            return 0, "", ""

        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(native, "default_runner", fake_default_runner):
                ad = boot._native_boot_adapter(_config(tmp), os.path.join(tmp, "transaction"))
                ad.finalize()                                    # the proxy start
                ad._systemctl("start", "bloxos-hub.service")     # a backend start
                ad._systemctl("daemon-reload")
        # The proxy start is enqueued non-blocking (the gate is ordered before it).
        self.assertIn(["systemctl", "start", "--no-block", "caddy.service"], recorded)
        self.assertNotIn(["systemctl", "start", "caddy.service"], recorded)
        # Backend starts and daemon-reload pass through REAL (ungated, no deadlock).
        self.assertIn(["systemctl", "start", "bloxos-hub.service"], recorded)
        self.assertIn(["systemctl", "daemon-reload"], recorded)

    def test_boot_adapter_keeps_backend_readiness_real(self):
        # No ready_check override: recovery must confirm the restored loopback
        # backends answer before finalize reopens the proxy.
        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(native, "default_runner", lambda cmd: (0, "", "")):
                ad = boot._native_boot_adapter(_config(tmp), os.path.join(tmp, "transaction"))
            self.assertIs(ad._ready, native._default_ready)


if __name__ == "__main__":
    unittest.main()
