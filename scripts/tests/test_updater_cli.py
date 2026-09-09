import argparse
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch, MagicMock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater import cli


class CLITests(unittest.TestCase):
    def test_release_entrypoint_preserves_failure_exit_status(self):
        with patch.object(cli, "main", return_value=1):
            with self.assertRaises(SystemExit) as result:
                cli.entrypoint()
        self.assertEqual(result.exception.code, 1)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bloxos-cli-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_worker_uses_shared_engine(self):
        with patch.object(cli.Config, "load", return_value=MagicMock()) as load, \
             patch.object(cli, "protected_directory"), patch.object(cli, "run_worker", return_value="succeeded") as worker:
            self.assertEqual(cli.worker(), 0)
            worker.assert_called_once_with(load.return_value)

    def test_failed_engine_is_nonzero(self):
        with patch.object(cli.Config, "load", return_value=MagicMock()), \
             patch.object(cli, "protected_directory"), patch.object(cli, "run_worker", return_value="rolled_back"):
            self.assertEqual(cli.worker(), 1)

    def test_failed_setup_keeps_retry_marker_and_no_capability(self):
        pending = self.root / "setup.pending.json"
        pending.write_text("{}")
        config = {"mode": "native", "hub_unit": "bloxos-hub.service", "mailbox_dir": str(self.root / "mailbox")}
        with patch.object(cli, "CONFIG", self.root / "config.json"), \
             patch.object(cli, "install_units"), patch.object(cli, "run", side_effect=RuntimeError("restart failed")):
            with self.assertRaises(RuntimeError):
                cli.finish_setup(config)
        self.assertTrue(pending.exists())
        self.assertFalse((self.root / "mailbox/outbox/capabilities.json").exists())

    def test_successful_setup_publishes_capability_last(self):
        pending = self.root / "setup.pending.json"
        pending.write_text("{}")
        config = {"mode": "native", "hub_unit": "bloxos-hub.service", "mailbox_dir": str(self.root / "mailbox")}
        with patch.object(cli, "CONFIG", self.root / "config.json"), \
             patch.object(cli, "install_units"), patch.object(cli, "run"):
            cli.finish_setup(config)
        self.assertFalse(pending.exists())
        self.assertTrue(json.loads((self.root / "mailbox/outbox/capabilities.json").read_text())["enabled"])

    def test_ambiguous_native_is_not_guessed(self):
        args = argparse.Namespace(mode="native", directory=str(self.root), public_url=None, ca_file=None, yes=True)
        with patch.object(cli, "CONFIG", self.root / "config.json"), \
             patch.object(cli.sys, "platform", "linux"), patch.object(cli.shutil, "which", return_value="systemctl"), \
             patch.object(cli, "systemd_active", return_value=True), \
             patch("updater.native.discover_native", return_value={"ambiguous": True}):
            with self.assertRaisesRegex(RuntimeError, "Cannot prove"):
                cli.initialize(args)
        self.assertFalse((self.root / "config.json").exists())

    def test_request_is_complete_before_publication(self):
        config = MagicMock(mailbox_dir=str(self.root / "mailbox"))
        inbox = self.root / "mailbox/inbox"
        inbox.mkdir(parents=True)
        seen = []
        real_link = cli.os.link
        def publish(source, destination, **kwargs):
            body = json.loads(Path(source).read_text())
            seen.append(body)
            real_link(source, destination, **kwargs)
            outbox = self.root / "mailbox/outbox"
            outbox.mkdir()
            (outbox / "status.json").write_text(json.dumps({"request_id": body["request_id"], "state": "succeeded"}))
        synced = []
        def sync(path):
            self.assertEqual(Path(path), inbox / "request.json")
            self.assertTrue(Path(path).exists())
            self.assertEqual(list(inbox.glob(".request-*")), [])
            synced.append(path)
        def start(_args):
            self.assertEqual(synced, [str(inbox / "request.json")])
        with patch.object(cli.Config, "load", return_value=config), patch.object(cli.os, "link", side_effect=publish), patch.object(cli, "_fsync_parent", side_effect=sync), patch.object(cli, "run", side_effect=start):
            self.assertEqual(cli.request_update(), 0)
        self.assertEqual(seen[0]["target_version"], "latest")
        self.assertEqual(list(inbox.glob(".request-*")), [])


class InstallUnitsTests(unittest.TestCase):
    """The per-mode systemd wiring for boot recovery (PR#217)."""

    def _run(self, config):
        writes, runs = {}, []
        with patch.object(cli, "write_file", side_effect=lambda p, b, *a, **k: writes.__setitem__(str(p), b)), \
             patch.object(cli, "run", side_effect=lambda cmd, *a, **k: runs.append(cmd)):
            cli.install_units(config)
        return writes, runs

    def test_native_gates_proxy_and_leaves_worker_off_boot(self):
        writes, runs = self._run({"mailbox_dir": "/m", "mode": "native", "proxy_unit": "caddy.service"})
        gate = writes["/etc/systemd/system/bloxos-updater-recovery.service"]
        self.assertIn("RemainAfterExit=yes", gate)
        self.assertIn("recover-boot", gate)
        self.assertIn("WantedBy=multi-user.target", gate)
        self.assertNotIn("systemctl", gate)  # the gate never starts the worker
        worker = writes["/etc/systemd/system/bloxos-updater.service"]
        self.assertIn("Requires=bloxos-updater-recovery.service", worker)
        self.assertIn("After=network-online.target bloxos-updater-recovery.service", worker)
        # NOT boot-enabled: no [Install] section on the runtime worker in native.
        self.assertNotIn("[Install]", worker)
        # The proxy is gated fail-closed.
        dropin = writes["/etc/systemd/system/caddy.service.d/05-bloxos-updater-recovery-order.conf"]
        self.assertEqual(dropin, "[Unit]\nRequires=bloxos-updater-recovery.service\nAfter=bloxos-updater-recovery.service\n")
        self.assertIn(["systemctl", "enable", "bloxos-updater-recovery.service"], runs)
        self.assertIn(["systemctl", "disable", "bloxos-updater.service"], runs)
        self.assertNotIn(["systemctl", "enable", "bloxos-updater.service"], runs)
        self.assertIn(["systemctl", "enable", "--now", "bloxos-updater.path"], runs)

    def test_compose_keeps_worker_on_boot_and_orders_after_docker(self):
        writes, runs = self._run({"mailbox_dir": "/m", "mode": "compose"})
        gate = writes["/etc/systemd/system/bloxos-updater-recovery.service"]
        self.assertIn("After=network-online.target docker.service", gate)
        worker = writes["/etc/systemd/system/bloxos-updater.service"]
        # Boot-enabled in compose: restores suppressed restart policies + picks up
        # a queued request across a reboot.
        self.assertIn("[Install]\nWantedBy=multi-user.target", worker)
        self.assertIn("Requires=bloxos-updater-recovery.service", worker)
        # No systemd app units to gate under compose.
        self.assertNotIn("/etc/systemd/system/caddy.service.d/05-bloxos-updater-recovery-order.conf",
                         writes)
        self.assertFalse(any(".d/05-bloxos-updater-recovery-order.conf" in p for p in writes))
        self.assertIn(["systemctl", "enable", "bloxos-updater.service"], runs)
        self.assertIn(["systemctl", "enable", "bloxos-updater-recovery.service"], runs)

    def test_recover_boot_command_dispatches_to_gate(self):
        with patch.object(cli, "CONFIG", self.root / "config.json"), \
             patch.object(cli.os, "geteuid", return_value=0):
            (self.root / "config.json").write_text("{}")
            with patch.object(cli.Config, "load", return_value=MagicMock()) as load, \
                 patch("updater.boot.recover_boot", return_value=0) as gate:
                self.assertEqual(cli.main(["recover-boot"]), 0)
                gate.assert_called_once_with(load.return_value)

    def test_recover_boot_missing_config_fails_closed(self):
        # The gate exists only on a configured host, so a missing config is
        # damage, not proof of no pending transaction: fail closed (nonzero) so
        # the proxy's Requires= keeps traffic shut.
        with patch.object(cli, "CONFIG", self.root / "absent.json"), \
             patch.object(cli.os, "geteuid", return_value=0):
            self.assertEqual(cli.main(["recover-boot"]), 1)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bloxos-cli-iu-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)


if __name__ == "__main__":
    unittest.main()
