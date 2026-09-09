"""Offline fixtures only: no Docker, server, signing key, network or agent execution."""
import hashlib
import json
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
from unittest import mock

import agent_bundle as bundle


def fixture(platform, release=7):
    data = bytearray(128)
    if platform.startswith("linux/"):
        data[:6] = b"\x7fELF\x02\x01"
        struct.pack_into("<H", data, 18, 62 if platform == "linux/amd64" else 183)
    else:
        data[:2] = b"MZ"
        struct.pack_into("<I", data, 60, 64)
        data[64:68] = b"PE\0\0"
        struct.pack_into("<H", data, 68, 0x8664)
        struct.pack_into("<H", data, 88, 0x20B)
    return data + f"BLOXOS-AGENT-RELEASE:{release:010d}:".encode()


class BundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.addCleanup(self.unlock)
        self.source = self.root / "download"
        self.source.mkdir()
        self.staging = self.root / "staging"
        self.staging.mkdir()
        self.active = self.root / "active"
        self.active.mkdir()
        (self.active / "bloxos-agent").write_bytes(b"existing agent")
        for platform, name in bundle.FILES.items():
            (self.source / name).write_bytes(fixture(platform))
        self.sha = bundle.create_manifest(self.source, "a" * 40, "v1.2.1", "sha256:" + "b" * 64)

    def unlock(self):
        for path in self.root.rglob("*"):
            if path.is_dir() and not path.is_symlink():
                path.chmod(0o700)

    def test_check_is_read_only_and_reports_three_targets(self):
        before = {p.name: p.read_bytes() for p in self.source.iterdir()}
        got = bundle.check(self.source, self.sha)
        self.assertEqual(got["agent_release"], 7)
        self.assertEqual(set(got["artifacts"]), set(bundle.FILES))
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.source.iterdir()})
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_stage_copies_only_to_new_versioned_directory(self):
        dest = bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(dest, self.staging.resolve() / "agent-release-7")
        self.assertEqual(bundle.check(dest, self.sha), bundle.check(self.source, self.sha))
        self.assertEqual((self.active / "bloxos-agent").read_bytes(), b"existing agent")
        self.assertEqual(dest.stat().st_mode & 0o777, 0o555)
        self.assertTrue(all(p.stat().st_mode & 0o777 == 0o444 for p in dest.iterdir()))

    def test_repeated_stage_never_overwrites_existing_release(self):
        dest = bundle.stage(self.source, self.sha, self.staging)
        with self.assertRaisesRegex(ValueError, "already staged"):
            bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(bundle.check(dest, self.sha)["agent_release"], 7)

    def test_empty_existing_directory_and_dangling_symlink_are_preserved(self):
        dest = self.staging / "agent-release-7"
        dest.mkdir()
        with self.assertRaisesRegex(ValueError, "already staged"):
            bundle.stage(self.source, self.sha, self.staging)
        self.assertTrue(dest.is_dir())
        dest.rmdir()
        dest.symlink_to(self.root / "nonexistent")
        with self.assertRaisesRegex(ValueError, "already staged"):
            bundle.stage(self.source, self.sha, self.staging)
        self.assertTrue(dest.is_symlink())

    def test_shared_writable_staging_root_rejected(self):
        self.staging.chmod(0o777)
        with self.assertRaisesRegex(ValueError, "not writable by group/others"):
            bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_wrong_manifest_hash_creates_nothing(self):
        with self.assertRaisesRegex(ValueError, "manifest SHA256 mismatch"):
            bundle.stage(self.source, "0" * 64, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_modified_binary_is_rejected(self):
        p = self.source / bundle.FILES["linux/amd64"]
        p.write_bytes(p.read_bytes() + b"changed")
        with self.assertRaisesRegex(ValueError, "artifact SHA256"):
            bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_missing_arm64_rejected(self):
        (self.source / bundle.FILES["linux/arm64"]).unlink()
        with self.assertRaises(FileNotFoundError):
            bundle.check(self.source, self.sha)

    def test_architecture_and_marker_contract(self):
        with self.assertRaisesRegex(ValueError, "wrong ELF"):
            bundle.identity(fixture("linux/amd64"), "linux/arm64")
        with self.assertRaisesRegex(ValueError, "PE"):
            bundle.identity(fixture("linux/amd64"), "windows/amd64")
        for suffix in (b"", b"BLOXOS-AGENT-RELEASE:0000000000:",
                       b"BLOXOS-AGENT-RELEASE:broken:",
                       b"BLOXOS-AGENT-RELEASE:0000000007:" * 2):
            with self.subTest(suffix=suffix), self.assertRaisesRegex(ValueError, "release markers"):
                bundle.identity(fixture("linux/amd64")[:128] + suffix, "linux/amd64")

    def test_mixed_release_numbers_rejected(self):
        (self.source / bundle.FILES["linux/arm64"]).write_bytes(fixture("linux/arm64", 8))
        with self.assertRaisesRegex(ValueError, "different agent release"):
            bundle.check(self.source, self.sha)

    def test_manifest_release_and_extra_targets_cannot_lie(self):
        original = json.loads((self.source / bundle.MANIFEST).read_bytes())
        for change in ({"agent_release": 8}, {"artifacts": {"../escape": {}}}):
            data = json.dumps({**original, **change}).encode()
            (self.source / bundle.MANIFEST).write_bytes(data)
            with self.assertRaises(ValueError):
                bundle.check(self.source, hashlib.sha256(data).hexdigest())

    def test_no_symlink_or_fifo_input(self):
        p = self.source / bundle.FILES["linux/amd64"]
        p.unlink()
        p.symlink_to(self.active / "bloxos-agent")
        with self.assertRaises(OSError):
            bundle.check(self.source, self.sha)
        p.unlink()
        os.mkfifo(p)
        with self.assertRaisesRegex(ValueError, "not a regular file"):
            bundle.check(self.source, self.sha)

    def test_active_environment_directory_rejected(self):
        with mock.patch.dict(os.environ, {"BLOXOS_AGENT_BINARY": str(self.active / "bloxos-agent")}):
            with self.assertRaisesRegex(ValueError, "active agent directory"):
                bundle.stage(self.source, self.sha, self.active)
        self.assertEqual((self.active / "bloxos-agent").read_bytes(), b"existing agent")

    def test_future_active_override_under_staging_root_rejected(self):
        for env, filename in zip(
                ("BLOXOS_AGENT_BINARY", "BLOXOS_AGENT_BINARY_ARM64", "BLOXOS_AGENT_BINARY_WINDOWS"),
                bundle.FILES.values()):
            with self.subTest(env=env):
                target = self.staging / "agent-release-7" / filename
                with mock.patch.dict(os.environ, {env: str(target)}):
                    with self.assertRaisesRegex(ValueError, "active agent directory"):
                        bundle.stage(self.source, self.sha, self.staging)
                self.assertFalse(target.exists())
                self.assertEqual(list(self.staging.iterdir()), [])

    def test_sibling_of_active_directory_can_be_staged(self):
        with mock.patch.dict(os.environ, {"BLOXOS_AGENT_BINARY": str(self.active / "bloxos-agent")}):
            self.assertTrue(bundle.stage(self.source, self.sha, self.staging).is_dir())

    def test_whitespace_wrapped_future_override_cannot_activate(self):
        for env in ("BLOXOS_AGENT_BINARY", "BLOXOS_AGENT_BINARY_ARM64", "BLOXOS_AGENT_BINARY_WINDOWS"):
            for padding in (" ", "\t\n", "\u0085\u00a0\u2003\u3000"):
                with self.subTest(env=env, padding=repr(padding)):
                    target = self.staging / "agent-release-7" / "bloxos-agent-linux-amd64"
                    with mock.patch.dict(os.environ, {env: padding + str(target) + padding}):
                        with self.assertRaisesRegex(ValueError, "active agent directory"):
                            bundle.stage(self.source, self.sha, self.staging)
                    self.assertEqual(list(self.staging.iterdir()), [])

    def test_relative_visible_override_is_rejected(self):
        with mock.patch.dict(os.environ, {"BLOXOS_AGENT_BINARY": " ../active/bloxos-agent "}):
            with self.assertRaisesRegex(ValueError, "absolute path"):
                bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_whitespace_only_override_matches_hub_unset_behavior(self):
        with mock.patch.dict(os.environ, {"BLOXOS_AGENT_BINARY": " \t\n\u00a0"}):
            self.assertTrue(bundle.stage(self.source, self.sha, self.staging).is_dir())

    def test_source_change_during_copy_leaves_no_stage(self):
        original = bundle.read_regular
        counts = {}
        def read(path, limit):
            key = str(path)
            counts[key] = counts.get(key, 0) + 1
            data = original(path, limit)
            if path.name == bundle.FILES["linux/amd64"] and path.parent == self.source and counts[key] == 2:
                data += b"changed between validation and copying"
            return data
        with mock.patch.object(bundle, "read_regular", side_effect=read):
            with self.assertRaisesRegex(ValueError, "artifact SHA256"):
                bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])

    def test_copy_failure_preserves_existing_files(self):
        with mock.patch.object(Path, "rename", side_effect=OSError("disk failure")):
            with self.assertRaisesRegex(OSError, "disk failure"):
                bundle.stage(self.source, self.sha, self.staging)
        self.assertEqual(list(self.staging.iterdir()), [])
        self.assertEqual((self.active / "bloxos-agent").read_bytes(), b"existing agent")

    def test_manifest_creation_never_overwrites(self):
        with self.assertRaises(FileExistsError):
            bundle.create_manifest(self.source, "a" * 40, "v1.2.1", "sha256:" + "b" * 64)

    def test_release_tag_contract(self):
        for tag in ("v1.2.1", "v1.2.1-rc.1", "v2.0.0-preview-2"):
            self.assertEqual(bundle.validate_tag(tag), tag)
        for tag in (None, "vnext", "v1.2", "v١.٢.٣", "v1.２.3", "v1.2.٣", "v1.2.1+build.1", "v1.2.1-", "v1.2.1-rc..1", "v1.2.1-" + "x" * 130):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                bundle.validate_tag(tag)

    def run_export(self, failure="", release_tag="v1.2.1"):
        bin_dir = self.root / "bin"
        bin_dir.mkdir()
        docker = bin_dir / "docker"
        docker.write_text('''#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >> "$DOCKER_LOG"
case "$1" in
  pull) test "$2" = --platform; test "$3" = linux/amd64 ;;
  image) if [[ "$FAIL_EXPORT" == revision ]]; then printf 'wrong'; else printf '%040d' 0; fi ;;
  create) test "$2" = --platform; test "$3" = linux/amd64
          test "$4" = --network; test "$5" = none; printf '%064d' 0 ;;
  cp) [[ "$FAIL_EXPORT" != copy ]] || exit 9
      case "$2" in
        *:/usr/local/lib/bloxos/linux/amd64/bloxos-agent) name=bloxos-agent-linux-amd64 ;;
        *:/usr/local/lib/bloxos/linux/arm64/bloxos-agent) name=bloxos-agent-linux-arm64 ;;
        *:/usr/local/lib/bloxos/windows/bloxos-agent.exe) name=bloxos-agent-windows-amd64.exe ;;
        *) exit 10 ;;
      esac
      cp "$FIXTURES/$name" "$3" ;;
  rm) test "$2" = -v; test ${#3} = 64 ;;
  *) exit 11 ;;
esac
''')
        docker.chmod(0o700)
        output = self.root / "export"
        log = self.root / "docker.log"
        result = subprocess.run([
            "bash", str(Path(__file__).with_name("export-agent-bundle.sh")),
            "ghcr.io/example/hub@sha256:" + "b" * 64, "0" * 40, release_tag, str(output),
        ], env={**os.environ, "PATH": str(bin_dir) + os.pathsep + os.environ["PATH"],
                "DOCKER_LOG": str(log), "FIXTURES": str(self.source), "FAIL_EXPORT": failure},
            capture_output=True, text=True)
        return result, output, log.read_text() if log.exists() else ""

    def test_export_prerelease_roundtrip(self):
        result, output, _ = self.run_export(release_tag="v1.2.1-rc.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        sha = (output / "agent-manifest.sha256").read_text().split()[0]
        self.assertEqual(bundle.check(output, sha)["version"], "v1.2.1-rc.1")

    def test_export_invalid_tag_stops_before_docker(self):
        result, output, log = self.run_export(release_tag="vnext")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(output.exists())
        self.assertEqual(log, "")

    def test_export_unicode_digits_stops_before_docker(self):
        result, output, log = self.run_export(release_tag="v١.٢.٣")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(output.exists())
        self.assertEqual(log, "")

    def test_export_extracts_all_targets_without_starting_container(self):
        result, output, log = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        sha = (output / "agent-manifest.sha256").read_text().split()[0]
        self.assertEqual(bundle.check(output, sha)["agent_release"], 7)
        self.assertEqual(sum(line.startswith("cp ") for line in log.splitlines()), 3)
        self.assertTrue(log.splitlines()[-1].startswith("rm -v "))
        self.assertNotIn("start ", log)
        self.assertNotIn("run ", log)

    def test_export_revision_mismatch_never_creates_container_or_output(self):
        result, output, log = self.run_export("revision")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(output.exists())
        self.assertNotIn("create ", log)

    def test_export_copy_failure_removes_only_created_container(self):
        result, output, log = self.run_export("copy")
        self.assertEqual(result.returncode, 9, result.stderr)
        self.assertFalse((output / bundle.MANIFEST).exists())
        self.assertTrue(log.splitlines()[-1].startswith("rm -v "))
        self.assertEqual((self.active / "bloxos-agent").read_bytes(), b"existing agent")


if __name__ == "__main__":
    unittest.main()
