#!/usr/bin/env python3
"""Boot smoke for the packaged hub: does the delivery gate actually gate?

Every other test in this area checks loadAgentBundle as a function. None of
them can catch the two ways this feature silently stops existing:

  * the -ldflags marker is not set, so agentBundleIsRequired() is false and a
    missing bundle is waved through. That was the real state of the tree until
    today — the flag was set by nothing at all.
  * the call at the top of main() is moved, removed, or ends up after the
    database opens, so a rejected candidate has already mutated installation
    state by the time anyone notices.

So this runs the real hub BINARY and checks process-level outcomes. The
invariant it asserts is the one that matters for rollback: a hub that cannot
prove its agent payloads exits BEFORE creating the database, so the updater's
readiness check can reject the candidate and roll back to a release that works.

WHICH BINARY MATTERS. Built from source with -X supplied here, this proves the
gate is wired and reachable. It does NOT prove the release build sets the flag
— hardcoding the flag cannot detect a Dockerfile that dropped it. Pass
--hub-binary to run the same cases against a hub extracted from the actual
built image, which is the only form that verifies release build flags.

Needs root-owned fixture paths, because the trusted-binary rule requires root
ownership and no group/other write on every ancestor. /tmp is world-writable,
so fixtures live in a unique directory under a root-owned parent.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent))

import agent_bundle as bundle
from test_agent_bundle import fixture

REPO = Path(__file__).resolve().parent.parent
# Root-owned and not group/other writable on every supported runner, which the
# trusted-path rule requires of every ancestor of a payload.
FIXTURE_PARENT = Path("/opt")
RELEASE = 12
READY_TIMEOUT = 60.0

# A skip is indistinguishable from a pass in a CI summary, and this file exists
# precisely to catch a check that has quietly stopped running. GitHub runners
# are not root, so without this the whole thing would skip in CI forever and
# report green. CI sets BLOXOS_BOOT_SMOKE_REQUIRE_ROOT=1 and invokes under
# sudo; if root or the toolchain is then missing, that is a failure, not a skip.
REQUIRE_ROOT = os.environ.get("BLOXOS_BOOT_SMOKE_REQUIRE_ROOT") == "1"
HUB_BINARY_ENV = "BLOXOS_BOOT_SMOKE_HUB_BINARY"


def prebuilt():
    """The hub to test, if one was supplied. Read late, never cached.

    Deliberately a function. When this was a module-level constant paired with
    a @skipIf decorator, both were evaluated at IMPORT — before __main__ ever
    parsed --hub-binary. On a runner with no Go toolchain that meant a
    perfectly valid --hub-binary still reported "needs the go toolchain", and
    under REQUIRE_ROOT it raised before the option could be read at all.
    """
    return os.environ.get(HUB_BINARY_ENV, "")


def unavailable():
    """Why this cannot run here, or None. Raises when CI demanded it run."""
    supplied = prebuilt()
    reason = None
    if os.geteuid() != 0:
        reason = "needs root to build root-owned fixture paths"
    elif supplied and not Path(supplied).is_file():
        reason = f"--hub-binary {supplied} does not exist"
    elif not supplied and not shutil.which("go"):
        reason = "needs the go toolchain, or --hub-binary"
    if reason and REQUIRE_ROOT:
        raise AssertionError(
            f"BLOXOS_BOOT_SMOKE_REQUIRE_ROOT=1 but the smoke cannot run: {reason}. "
            "Skipping here would hide whether the boot gate works.")
    return reason


def free_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


class HubBootGateTests(unittest.TestCase):
    """The gate must stop the PROCESS, not merely return an error."""

    @classmethod
    def setUpClass(cls):
        # Availability is decided HERE, not at import, so it sees the final
        # --hub-binary. A class decorator could not: it runs before __main__.
        reason = unavailable()
        if reason:
            raise unittest.SkipTest(reason)
        supplied = prebuilt()

        # A unique directory, created by this run and removed by it. Never a
        # fixed path: a test that rmtree's a predictable location on entry will
        # eventually delete something an operator put there.
        cls.root = Path(tempfile.mkdtemp(prefix="bloxos-boot-smoke-", dir=FIXTURE_PARENT))
        cls.root.chmod(0o755)
        cls.scratch = tempfile.mkdtemp(prefix="bloxos-boot-scratch-")

        cls.binary = cls.root / "bloxos-hub-packaged"
        if supplied:
            # The form that actually verifies release build flags. No Go needed.
            shutil.copyfile(supplied, cls.binary)
            cls.provenance = f"prebuilt {supplied}"
        else:
            subprocess.run(
                ["go", "build", "-ldflags", "-X main.agentBundleRequired=packaged",
                 "-o", str(cls.binary), "."],
                cwd=REPO / "hub", check=True,
                env=dict(os.environ,
                         GOCACHE=os.path.join(cls.scratch, "gocache"),
                         GOMODCACHE=os.path.join(cls.scratch, "gomod"),
                         GOFLAGS="-buildvcs=false"))
            cls.provenance = "source build with -X supplied by this test"
        cls.binary.chmod(0o755)

    @classmethod
    def tearDownClass(cls):
        # Only what this run created.
        shutil.rmtree(cls.root, ignore_errors=True)
        shutil.rmtree(cls.scratch, ignore_errors=True)

    def release_tree(self, name):
        """A root-owned <release>/hub/{bloxos-hub,agents/} laid out like an install."""
        hub = self.root / name / "hub"
        agents = hub / "agents"
        agents.mkdir(mode=0o755, parents=True)
        for platform, payload in bundle.FILES.items():
            target = agents / payload
            target.write_bytes(fixture(platform, release=RELEASE))
            target.chmod(0o755)
        digest = bundle.create_manifest(agents, None, None, None)
        (agents / bundle.MANIFEST).chmod(0o644)
        bundle.check(agents, digest, require_provenance=False)
        shutil.copyfile(self.binary, hub / "bloxos-hub")
        (hub / "bloxos-hub").chmod(0o755)
        for path in (self.root / name, hub, agents):
            os.chown(path, 0, 0)
            path.chmod(0o755)
        return hub

    def start(self, hub):
        """Launch the hub in a scratch workdir. Returns (process, workdir, url)."""
        workdir = Path(tempfile.mkdtemp(prefix="hub-boot-", dir=self.scratch))
        port = free_port()
        url = f"http://127.0.0.1:{port}"
        environment = dict(os.environ, HOME=str(workdir),
                           HUB_LISTEN=f"127.0.0.1:{port}",
                           # Without one of these the hub refuses to start on
                           # CORS grounds, which has nothing to do with the gate.
                           PUBLIC_URL=url)
        process = subprocess.Popen([str(hub / "bloxos-hub")], cwd=workdir,
                                   env=environment, text=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        return process, workdir, url

    def drain(self, process):
        try:
            return process.communicate(timeout=READY_TIMEOUT)[0] or ""
        except subprocess.TimeoutExpired:
            process.kill()
            return process.communicate()[0] or ""

    def refuses(self, hub):
        """Run a hub expected to die at the gate; assert it did, before the DB."""
        process, workdir, _ = self.start(hub)
        output = self.drain(process)
        self.assertIsNotNone(process.poll(), "the hub must exit, not start")
        self.assertNotEqual(process.returncode, 0, output[-2000:])
        self.assertIn("agent delivery", output, output[-2000:])
        self.assertFalse((workdir / "bloxos.db").exists(),
                         "the database was created before the gate refused; a rejected "
                         "candidate must not mutate installation state")

    # The control. Without it every refusal below could be passing for an
    # unrelated reason — and "it created a database" is not enough, because a
    # hub can initialise one and then die seconds later. Readiness means the
    # process is serving.
    def test_a_valid_bundle_serves_traffic(self):
        process, workdir, url = self.start(self.release_tree("valid"))
        try:
            deadline = time.monotonic() + READY_TIMEOUT
            last = None
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    self.fail("the hub exited before becoming ready:\n"
                              + self.drain(process)[-3000:])
                try:
                    with urllib.request.urlopen(url + "/health", timeout=2) as answer:
                        if answer.status == 200:
                            break
                except (urllib.error.URLError, OSError, TimeoutError) as error:
                    last = error
                time.sleep(0.25)
            else:
                process.kill()
                self.fail(f"hub never became ready at {url}/health (last: {last})\n"
                          + self.drain(process)[-3000:])

            self.assertIsNone(process.poll(), "the hub must still be running once ready")
            self.assertTrue((workdir / "bloxos.db").exists())
        finally:
            process.kill()
            process.communicate()

    def test_a_missing_bundle_stops_the_process_before_the_database(self):
        """The case that needs the build-time marker to be set at all."""
        hub = self.release_tree("missing")
        shutil.rmtree(hub / "agents")
        self.refuses(hub)

    def test_a_corrupt_payload_stops_the_process_before_the_database(self):
        hub = self.release_tree("corrupt")
        (hub / "agents" / bundle.FILES["linux/arm64"]).write_bytes(b"corrupted in transit")
        self.refuses(hub)

    def test_a_payload_for_the_wrong_architecture_stops_the_process(self):
        """Only reading the image header can catch this one.

        The catalog is restamped BY HAND rather than regenerated: create_manifest
        runs the same architecture check, so regenerating would fail in Python
        and the hub process would never start — the test would pass without the
        gate under test ever running.
        """
        hub = self.release_tree("wrongarch")
        agents = hub / "agents"
        payload = agents / bundle.FILES["linux/arm64"]
        body = fixture("linux/amd64", release=RELEASE)
        payload.write_bytes(body)

        catalog = json.loads((agents / bundle.MANIFEST).read_text())
        catalog["artifacts"]["linux/arm64"] = {
            "file": bundle.FILES["linux/arm64"],
            "size": len(body),
            "sha256": hashlib.sha256(body).hexdigest(),
        }
        (agents / bundle.MANIFEST).write_text(json.dumps(catalog, sort_keys=True, indent=2) + "\n")
        (agents / bundle.MANIFEST).chmod(0o644)

        self.refuses(hub)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hub-binary", default=None,
                        help="hub extracted from a built image; the only form that "
                             "verifies the release build actually sets the marker")
    known, rest = parser.parse_known_args()
    if known.hub_binary:
        os.environ[HUB_BINARY_ENV] = known.hub_binary
    unittest.main(argv=[sys.argv[0]] + rest, verbosity=2)
