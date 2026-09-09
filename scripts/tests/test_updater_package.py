import importlib.util
import json
from pathlib import Path
import tarfile
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("server_bundle", Path(__file__).resolve().parents[1] / "export-server-bundle.py")
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class PackageTests(unittest.TestCase):
    def test_platform_children_are_distinct_and_attestations_ignored(self):
        index = {"manifests": [
            {"platform": {"os": "linux", "architecture": "amd64"}, "digest": "sha256:" + "a" * 64},
            {"platform": {"os": "linux", "architecture": "arm64", "variant": "v8"}, "digest": "sha256:" + "b" * 64},
            {"platform": {"os": "unknown", "architecture": "unknown"}, "digest": "sha256:" + "c" * 64},
        ]}
        image = "ghcr.io/bokiko/bloxos-hub@sha256:" + "d" * 64
        with patch.object(bundle, "run", return_value=json.dumps(index)) as run:
            for arch, digest in (("amd64", "a"), ("arm64", "b")):
                self.assertEqual(bundle.platform_image(image, "linux/" + arch),
                                 "ghcr.io/bokiko/bloxos-hub@sha256:" + digest * 64)
            run.assert_called_with(["docker", "manifest", "inspect", image])

    def test_missing_ambiguous_or_invalid_child_fails_closed(self):
        valid = {"platform": {"os": "linux", "architecture": "arm64"}, "digest": "sha256:" + "a" * 64}
        for entries in ([], [valid, valid], [dict(valid, digest="latest")],
                        [dict(valid, platform={"os": "linux", "architecture": "arm64", "variant": "v9"})]):
            with self.subTest(entries=entries), patch.object(bundle, "run", return_value=json.dumps({"manifests": entries})):
                with self.assertRaises(ValueError):
                    bundle.platform_image("ghcr.io/bokiko/bloxos-hub@sha256:" + "d" * 64, "linux/arm64")

    def test_export_uses_child_for_pull_inspect_and_create(self):
        child = "ghcr.io/bokiko/bloxos-hub@sha256:" + "b" * 64
        with patch.object(bundle, "platform_image", return_value=child), \
             patch.object(bundle, "run", side_effect=["linux/arm64", "revision", "c" * 64]) as run, \
             patch.object(bundle.subprocess, "run") as command:
            bundle.export("index", "linux/arm64", "/binary", Path("/out"), "revision")
            self.assertEqual(command.call_args_list[0].args[0], ["docker", "pull", "--platform", "linux/arm64", child])
            self.assertEqual(run.call_args_list[-1].args[0], ["docker", "create", "--platform", "linux/arm64", "--network", "none", child])

    def test_wrong_child_platform_is_rejected_before_create(self):
        with patch.object(bundle, "platform_image", return_value="child"), \
             patch.object(bundle, "run", return_value="linux/amd64"), \
             patch.object(bundle.subprocess, "run") as command:
            with self.assertRaisesRegex(ValueError, "platform"):
                bundle.export("index", "linux/arm64", "/binary", Path("/out"), "revision")
            self.assertEqual(command.call_count, 1)

    @unittest.skipUnless(shutil.which("node"), "Node runtime required")
    def test_pnpm_dependency_resolution_survives_export_and_extraction(self):
        sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
        from updater.engine import safe_extract_tar
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tree = root / "tree"
            (tree / "hub").mkdir(parents=True)
            modules = tree / "dashboard/node_modules"
            package_a = modules / ".pnpm/a/node_modules/a"
            package_b = modules / ".pnpm/b/node_modules/b"
            package_a.mkdir(parents=True)
            package_b.mkdir(parents=True)
            (package_a / "index.js").write_text("module.exports = require('b')")
            (package_b / "index.js").write_text("module.exports = 42")
            (package_a.parent / "b").symlink_to("../../b/node_modules/b")
            (modules / "a").symlink_to(".pnpm/a/node_modules/a")
            archive = root / "server.tar.gz"
            bundle.pack_tree(tree, archive)
            safe_extract_tar(str(archive), str(root / "out"))
            result = subprocess.check_output(["node", "-e", "console.log(require(process.argv[1]))", str(root / "out/dashboard/node_modules/a")], text=True)
            self.assertEqual(result.strip(), "42")

    def test_internal_links_keep_node_resolution_semantics(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            tree = root / "tree"
            (tree / "hub").mkdir(parents=True)
            (tree / "dashboard").mkdir()
            (tree / "hub/bloxos-hub").write_bytes(b"fixture")
            (tree / "dashboard/module.js").write_text("module.exports = {}")
            (tree / "dashboard/link.js").symlink_to("module.js")
            (tree / "dashboard/pruned.js").symlink_to("absent-traced-module.js")
            archive = root / "bundle.tar.gz"
            bundle.pack_tree(tree, archive)
            with tarfile.open(archive) as packed:
                self.assertTrue(all(item.isfile() or item.isdir() or item.issym() for item in packed))
                self.assertTrue(packed.getmember("dashboard/link.js").issym())
                self.assertTrue(packed.getmember("dashboard/pruned.js").issym())

    def test_external_link_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "tree/hub").mkdir(parents=True)
            (root / "tree/dashboard").mkdir()
            (root / "outside").write_text("private")
            (root / "tree/dashboard/escape").symlink_to(root / "outside")
            with self.assertRaisesRegex(ValueError, "escapes"):
                bundle.pack_tree(root / "tree", root / "bundle.tar.gz")


if __name__ == "__main__":
    unittest.main()
