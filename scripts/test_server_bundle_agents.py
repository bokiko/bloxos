#!/usr/bin/env python3
"""Old-worker transport smoke for agent payloads. No Docker, network or signing.

This runs BEFORE publication, deliberately. A tag-time test discovers a broken
release after the tag exists, and the fleet is exactly who finds out.

What it proves, end to end, with the real components:

  1. The release packer places the published agent bytes at hub/agents/.
  2. An UNCHANGED updater carries them through intact — both its raw
     safe_extract_tar and its real NativeAdapter.stage, which is the path that
     actually runs on a host: download, verify, extract, copytree, fsync.
  3. The new hub's own loader accepts the result, and fails closed when a
     required payload is missing or damaged.

Step 2 is the load-bearing one. The whole delivery design rests on the claim
that no updater change is needed, and this is what makes that a tested claim
rather than a reading of the source.

SCOPE, stated plainly so nothing here is mistaken for more than it is: this
file proves transport and loader acceptance. It does NOT prove hub readiness —
that the packaged build carries its -ldflags marker, or that the boot gate
refuses to start. A loader unit test cannot catch a missing build flag. That is
scripts/test_hub_bundle_boot.py, which runs the real hub process.
"""
import hashlib
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

import agent_bundle as bundle
from test_agent_bundle import fixture
from updater import engine

sys.path.insert(0, str(Path(__file__).resolve().parent / "tests"))
import test_updater_native as native_harness

# The packer's filename is hyphenated, so it is loaded by path rather than
# given an import-friendly alias in the repo.
_spec = importlib.util.spec_from_file_location(
    "export_server_bundle", Path(__file__).resolve().parent / "export-server-bundle.py")
export_server_bundle = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(export_server_bundle)

REPO = Path(__file__).resolve().parent.parent
SOURCE_SHA = "0" * 40
IMAGE_DIGEST = "sha256:" + "a" * 64
HUB_IMAGE = "ghcr.io/bokiko/bloxos-hub@" + IMAGE_DIGEST
VERSION = "v9.9.9"
RELEASE = 9


class ServerBundleAgentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

        # The published agent payloads, exactly as export-agent-bundle.sh
        # produces them: three binaries, a manifest, and the manifest's sidecar
        # SHA that is itself a release asset.
        self.agents = self.root / "native-agents"
        self.agents.mkdir()
        for platform, name in bundle.FILES.items():
            (self.agents / name).write_bytes(fixture(platform, release=RELEASE))
        digest = bundle.create_manifest(self.agents, SOURCE_SHA, VERSION, IMAGE_DIGEST)
        (self.agents / "agent-manifest.sha256").write_text(f"{digest}  agent-manifest.json\n")

        # A server tree shaped like an exported one.
        self.tree = self.root / "tree"
        (self.tree / "hub").mkdir(parents=True)
        (self.tree / "dashboard" / ".next").mkdir(parents=True)
        # A real ELF: the updater verifies the staged hub's architecture before
        # any downtime, so a placeholder never reaches the copytree under test.
        (self.tree / "hub" / "bloxos-hub").write_bytes(native_harness.fake_elf("amd64"))
        (self.tree / "dashboard" / "server.js").write_bytes(b"// standalone")
        (self.tree / "dashboard" / ".next" / "BUILD_ID").write_bytes(b"abc")

    def stage(self):
        return export_server_bundle.stage_agents(
            self.agents, self.tree, HUB_IMAGE, VERSION, SOURCE_SHA)

    def pack(self):
        manifest = self.stage()
        archive = self.root / "bloxos-server-linux-amd64.tar.gz"
        export_server_bundle.pack_tree(self.tree, archive)
        export_server_bundle.verify_packed_agents(archive, manifest)
        return archive

    def transport(self, archive):
        """Extract with the UNCHANGED updater the fleet already runs."""
        destination = self.root / "extracted"
        engine.safe_extract_tar(str(archive), str(destination))
        return destination

    def test_the_real_native_stage_publishes_the_agents(self):
        """The path a host actually takes, not just the extractor beneath it.

        safe_extract_tar alone leaves out everything NativeAdapter.stage does
        around it — the checksum gate, the validation that runs before any
        downtime, and the copytree into the versioned release directory. If any
        of those dropped or refused the new files, extraction tests would still
        pass and the fleet would still be offered stale agents.
        """
        archive = self.pack()
        with tempfile.TemporaryDirectory() as workdir:
            configuration = native_harness.native_config(workdir)
            adapter, manifest = native_harness.adapter_with_bundle(
                workdir, configuration, native_harness.Runner())
            # Serve the archive we just packed, under its real checksum.
            body = archive.read_bytes()
            manifest["native"]["amd64"]["sha256"] = hashlib.sha256(body).hexdigest()
            adapter._fetch = lambda url, limit: body

            adapter.stage(manifest, os.path.join(configuration["state_dir"], "transaction", "release"))
            release = Path(adapter.journal.get("release_dir"))

            # The hub runs from <release>/hub/bloxos-hub, so this is the exact
            # directory pairing the loader depends on.
            self.assertTrue((release / "hub" / "bloxos-hub").is_file())
            published = release / "hub" / "agents"
            for name in [bundle.MANIFEST, *bundle.FILES.values()]:
                self.assertEqual((published / name).read_bytes(), (self.agents / name).read_bytes(),
                                 f"{name} did not survive the real native stage")
            digest = (self.agents / "agent-manifest.sha256").read_text().split()[0]
            self.assertEqual(bundle.check(published, digest)["agent_release"], RELEASE)

    def test_published_bytes_reach_the_hub_directory_unchanged(self):
        extracted = self.transport(self.pack())
        packed = extracted / "hub" / "agents"

        for name in [bundle.MANIFEST, *bundle.FILES.values()]:
            self.assertTrue((packed / name).is_file(), f"{name} did not survive transport")
            self.assertEqual(
                hashlib.sha256((packed / name).read_bytes()).hexdigest(),
                hashlib.sha256((self.agents / name).read_bytes()).hexdigest(),
                f"{name} differs from the published standalone asset")

        # The hub binary's own directory is the one the loader searches, so
        # agents/ has to be its direct child and not a sibling of the tree root.
        self.assertTrue((extracted / "hub" / "bloxos-hub").is_file())

        # The payloads still pass the canonical check after the round trip.
        digest = (self.agents / "agent-manifest.sha256").read_text().split()[0]
        self.assertEqual(bundle.check(packed, digest)["agent_release"], RELEASE)

    def test_the_real_hub_loader_accepts_the_transported_tree(self):
        extracted = self.transport(self.pack())
        if not shutil.which("go"):
            self.skipTest("go toolchain not available")

        environment = dict(os.environ)
        environment["BLOXOS_TEST_PACKAGED_HUB_DIR"] = str(extracted / "hub")
        result = subprocess.run(
            ["go", "test", "-count=1", "-v", "-run", "TestPackagedTreeLoadsAndFailsClosed", "./"],
            cwd=REPO / "hub", env=environment, text=True, capture_output=True)
        # A skip would let this file report success while proving nothing, so
        # the driver asserts the test actually ran.
        self.assertIn("--- PASS: TestPackagedTreeLoadsAndFailsClosed",
                      result.stdout, result.stdout + result.stderr)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_a_server_archive_without_agents_is_refused(self):
        """The failure this whole change exists to prevent."""
        archive = self.root / "empty.tar.gz"
        export_server_bundle.pack_tree(self.tree, archive)
        manifest = bundle.check(self.agents, (self.agents / "agent-manifest.sha256").read_text().split()[0])
        with self.assertRaises(ValueError):
            export_server_bundle.verify_packed_agents(archive, manifest)

    def test_packing_refuses_payloads_that_fail_their_manifest(self):
        (self.agents / "bloxos-agent-linux-arm64").write_bytes(fixture("linux/arm64", release=RELEASE) + b"x")
        with self.assertRaises(ValueError):
            self.stage()

    def test_packing_refuses_a_payload_built_for_the_wrong_architecture(self):
        (self.agents / "bloxos-agent-linux-arm64").write_bytes(fixture("linux/amd64", release=RELEASE))
        with self.assertRaises(ValueError):
            self.stage()

    # A valid bundle is not automatically the RIGHT bundle. Packing last
    # release's agents beside this release's hub would produce a fresh release
    # that once again offers the fleet stale binaries.
    def test_an_agent_bundle_from_another_release_is_refused(self):
        for field, value in (("image_digest", "sha256:" + "b" * 64),
                             ("source", "1" * 40),
                             ("version", "v9.9.8")):
            with self.subTest(field=field):
                kwargs = {"hub_image": HUB_IMAGE, "version": VERSION, "revision": SOURCE_SHA}
                kwargs["hub_image" if field == "image_digest" else
                       "version" if field == "version" else "revision"] = (
                    "ghcr.io/bokiko/bloxos-hub@" + value if field == "image_digest" else value)
                tree = Path(self.temp.name) / ("tree-" + field)
                (tree / "hub").mkdir(parents=True)
                with self.assertRaises(ValueError) as caught:
                    export_server_bundle.stage_agents(self.agents, tree, **kwargs)
                self.assertIn("does not belong to this server release", str(caught.exception))

    # The archive is what actually ships, so its BYTES are what must match.
    # A name check would pass all of these.
    def test_a_tampered_archive_is_refused(self):
        manifest = self.stage()
        packed = self.tree / "hub" / "agents"
        cases = {
            "truncated payload": lambda: (packed / "bloxos-agent-linux-amd64").write_bytes(b"short"),
            "substituted payload": lambda: (packed / "bloxos-agent-linux-arm64").write_bytes(
                fixture("linux/arm64", release=RELEASE) + b"pad"),
            "rewritten catalog": lambda: (packed / bundle.MANIFEST).write_text("{}"),
        }
        for label, tamper in cases.items():
            with self.subTest(case=label):
                original = {p.name: p.read_bytes() for p in packed.iterdir()}
                tamper()
                archive = Path(self.temp.name) / (label.replace(" ", "-") + ".tar.gz")
                export_server_bundle.pack_tree(self.tree, archive)
                with self.assertRaises(ValueError):
                    export_server_bundle.verify_packed_agents(archive, manifest)
                for name, body in original.items():
                    (packed / name).write_bytes(body)


if __name__ == "__main__":
    unittest.main(verbosity=2)
