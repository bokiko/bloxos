import copy
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater.compose import ComposeAdapter, atomic_json


class ComposeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="bloxos-compose-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = {"mode": "compose", "compose_dir": "/existing/docker", "compose_project": "fleet",
                       "compose_files": ["/private/base.json"], "public_url": "https://hub.example",
                       "compose_override": str(self.root / "override.json")}
        self.adapter = ComposeAdapter(self.config, self.root)
        self.containers = {}
        for name in ("hub", "dashboard", "caddy"):
            self.containers[name] = {"Id": name + "-id", "Image": "sha256:" + "a" * 64,
                                     "State": {"Running": True},
                                     "Config": {"Labels": {"com.docker.compose.project": "fleet"},
                                                "Env": ["PUBLIC_URL=https://hub.example"]},
                                     "NetworkSettings": {"Ports": {"443/tcp": [{"HostPort": "443"}]}}, "Mounts": []}
        for service, target in (("hub", "/data"), ("caddy", "/data"), ("caddy", "/config")):
            self.containers[service]["Mounts"].append({"Type": "volume", "Destination": target,
                                                     "Name": "fleet_" + service + target.replace("/", "_")})
        self.rendered = {"services": {}, "volumes": {}}
        for service, container in self.containers.items():
            mounts = []
            for mount in container["Mounts"]:
                mounts.append({"target": mount["Destination"], "source": mount["Name"]})
                self.rendered["volumes"][mount["Name"]] = {"name": mount["Name"]}
            self.rendered["services"][service] = {"volumes": mounts}

    def preflight(self, remote=False):
        def command(args, **kwargs):
            if args[:3] == ["docker", "context", "inspect"]:
                return json.dumps([{"Endpoints": {"docker": {"Host": "ssh://other" if remote else "unix:///var/run/docker.sock"}}}])
            return json.dumps(self.rendered)
        with patch.object(self.adapter, "containers", return_value=self.containers), \
             patch.object(self.adapter, "verify_edge"), patch("updater.compose.run", side_effect=command):
            self.adapter.preflight()

    def test_existing_stack_records_exact_volume_identity(self):
        self.preflight()
        self.assertEqual(self.adapter.saved["volumes"]["hub-data"], "fleet_hub_data")
        self.assertTrue((self.root / "compose.json").exists())

    def test_remote_daemon_is_not_treated_as_local(self):
        with self.assertRaisesRegex(RuntimeError, "local Docker"):
            self.preflight(remote=True)

    def test_edge_keeps_system_trust_with_optional_private_ca(self):
        active = {"handler": "reverse_proxy", "upstreams": [{"dial": "hub:4000"}, {"dial": "dashboard:3000"}]}
        for ca in (b"", b"fixture-ca"):
            with self.subTest(private_ca=bool(ca)), patch("updater.compose.run", return_value=json.dumps(active)), \
                 patch.object(self.adapter, "local_ca", return_value=ca), \
                 patch("updater.compose.ssl.create_default_context") as context, \
                 patch("updater.compose.socket.create_connection"):
                self.adapter.verify_edge()
                context.assert_called_once_with()
                if ca:
                    context.return_value.load_verify_locations.assert_called_once_with(cadata="fixture-ca")
                else:
                    context.return_value.load_verify_locations.assert_not_called()

    def test_internal_only_stack_cannot_become_target(self):
        self.containers["caddy"]["NetworkSettings"]["Ports"] = {}
        with self.assertRaisesRegex(RuntimeError, "does not publish"):
            self.preflight()

    def test_changed_volume_is_rejected_before_update(self):
        self.rendered["volumes"]["fleet_hub_data"]["name"] = "different_database"
        with self.assertRaisesRegex(RuntimeError, "different data"):
            self.preflight()

    def test_bind_mount_data_requires_supported_adapter_not_guess(self):
        self.containers["hub"]["Mounts"][0]["Type"] = "bind"
        with self.assertRaisesRegex(RuntimeError, "named data volumes"):
            self.preflight()

    def test_install_preserves_previous_override_for_rollback(self):
        previous = {"services": {"hub": {"environment": {"BLOXOS_UPDATER_DIR": "/run/bloxos-updater"}, "image": "old-hub"}}}
        self.adapter.saved = {"override": copy.deepcopy(previous), "candidate": {"hub": "new-hub", "dashboard": "new-dashboard"}}
        self.adapter.install()
        self.assertEqual(self.adapter.saved["override"], previous)
        current = json.loads(self.adapter.override.read_text())
        self.assertEqual(current["services"]["hub"]["image"], "new-hub")
        self.assertEqual(current["services"]["hub"]["environment"], previous["services"]["hub"]["environment"])

    def test_untrusted_image_reference_never_pulled(self):
        with patch("updater.compose.run") as command:
            with self.assertRaisesRegex(RuntimeError, "Invalid release image"):
                self.adapter.stage({"images": {"hub": "attacker/image:latest", "dashboard": "anything"}}, "unused")
            command.assert_not_called()

    def test_quiesce_stops_edge_before_components(self):
        with patch("updater.compose.run") as command:
            self.adapter.quiesce()
        self.assertEqual(command.call_args_list[0].args[0][-2:], ["stop", "caddy"])
        self.assertEqual(command.call_args_list[1].args[0][-3:], ["stop", "hub", "dashboard"])

    def test_incomplete_backup_never_restored(self):
        with patch("updater.compose.run") as command:
            with self.assertRaisesRegex(RuntimeError, "complete snapshot"):
                self.adapter.rollback()
            command.assert_not_called()

    def test_atomic_config_write_leaves_no_partial_or_stale_temp(self):
        path = self.root / "config.json"
        (self.root / "config.json.new").write_text("interrupted older writer")
        atomic_json(path, {"value": 1})
        atomic_json(path, {"value": 2})
        self.assertEqual(json.loads(path.read_text()), {"value": 2})
        self.assertEqual(path.stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
