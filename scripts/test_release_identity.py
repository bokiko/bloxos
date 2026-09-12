#!/usr/bin/env python3
"""Offline tests for the release identity gate. No network: history is passed in."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from release_identity import (
    IdentityError,
    check_history,
    check_image_platforms,
    load_history,
)

PLATFORMS = ("linux/amd64", "linux/arm64", "windows/amd64")


def artifacts(seed):
    return {platform: {"file": platform, "size": 100 + index,
                       "sha256": f"{seed:02x}{index:02x}" + "0" * 60}
            for index, platform in enumerate(PLATFORMS)}


def catalog(release, seed):
    return {"schema": 1, "agent_release": release, "artifacts": artifacts(seed)}


def history(**releases):
    """releases maps "<number>" to one seed, or a list of seeds for a divergent one."""
    raw = {}
    for release, seeds in releases.items():
        raw[release] = [artifacts(seed) for seed in (seeds if isinstance(seeds, list) else [seeds])]
    return load_history(raw)


class HistoryTests(unittest.TestCase):
    def test_a_new_release_above_the_floor_passes(self):
        self.assertIn("above the floor", check_history(catalog(9, 0xaa), history(**{"8": 0xbb})))

    def test_reusing_a_number_with_different_bytes_is_refused(self):
        with self.assertRaises(IdentityError) as caught:
            check_history(catalog(8, 0xaa), history(**{"8": 0xbb}))
        self.assertIn("DIFFERENT bytes", str(caught.exception))

    # A re-run of a failed workflow must not be a dead end.
    def test_retrying_a_release_with_identical_bytes_is_idempotent(self):
        self.assertIn("idempotent", check_history(catalog(8, 0xaa), history(**{"8": 0xaa})))

    # Numbers absent from history, so this exercises the FLOOR rule rather than
    # the conflicting-bytes rule; a number already in history is refused by a
    # different branch with a different message.
    def test_an_unused_number_at_or_below_the_floor_is_refused(self):
        for release in (5, 7):
            with self.subTest(release=release), self.assertRaises(IdentityError) as caught:
                check_history(catalog(release, 0xcc), history(**{"6": 0x11, "8": 0x22}))
            self.assertIn("floor", str(caught.exception).lower())




class DivergentHistoryTests(unittest.TestCase):
    """Ambiguity is detected FROM THE DATA, with no release number special-cased.

    Release 7 was published more than once with different bytes before this
    gate existed. Hardcoding that fact would have handled exactly one case and
    silently missed the next, so the gate finds it generically — and would find
    the same problem at any other number.
    """

    def test_any_ambiguous_number_can_never_be_reused(self):
        divergent = history(**{"7": [0xaa, 0xbb]})
        # Even bytes matching one of the published variants: what that number
        # identifies is no longer decidable, so "it matches" means nothing.
        for seed in (0xaa, 0xbb, 0xcc):
            with self.subTest(seed=seed), self.assertRaises(IdentityError) as caught:
                check_history(catalog(7, seed), divergent)
            self.assertIn("never be reused", str(caught.exception))

    def test_ambiguity_is_not_limited_to_release_seven(self):
        with self.assertRaises(IdentityError) as caught:
            check_history(catalog(12, 0xaa), history(**{"12": [0xaa, 0xbb]}))
        self.assertIn("ambiguous", str(caught.exception))

    # The failure mode to avoid: a gate permanently stuck because the past is
    # inconsistent.
    def test_inconsistent_history_does_not_block_future_releases(self):
        self.assertIn("above the floor",
                      check_history(catalog(8, 0xcc), history(**{"7": [0xaa, 0xbb]})))

    def test_a_divergent_release_still_raises_the_floor(self):
        with self.assertRaises(IdentityError):
            check_history(catalog(6, 0xcc), history(**{"7": [0xaa, 0xbb]}))

    # Collapsing history to one map per number would overwrite the first
    # catalog with the second, and the divergence would vanish before the check
    # that exists to find it ever ran.
    def test_history_retains_every_catalog_for_a_number(self):
        self.assertEqual(len(history(**{"7": [0xaa, 0xbb]})[7]), 2)


class FloorAndDowngradeTests(unittest.TestCase):
    def test_an_empty_history_fails_closed(self):
        """This repository HAS published releases, so nothing found means the
        lookup failed — and a failed lookup must never establish a floor of 0
        and approve any number at all."""
        with self.assertRaises(IdentityError) as caught:
            check_history(catalog(9, 0xaa), {})
        self.assertIn("lookup failed", str(caught.exception))

    def test_re_promoting_an_older_identical_release_is_refused(self):
        """Idempotence must not become a downgrade.

        Re-running release 8 after 9 exists changes nothing about 8, but
        promoting it now would offer the fleet an older identity than the one
        already published.
        """
        with self.assertRaises(IdentityError) as caught:
            check_history(catalog(8, 0xaa), history(**{"8": 0xaa, "9": 0xbb}))
        self.assertIn("lower the offered release identity", str(caught.exception))

    def test_re_promoting_the_current_release_is_still_idempotent(self):
        self.assertIn("idempotent",
                      check_history(catalog(9, 0xbb), history(**{"8": 0xaa, "9": 0xbb})))


class ImagePlatformTests(unittest.TestCase):
    """Sharing a hub image index does not make the embedded payloads equal."""

    def test_matching_platforms_pass(self):
        candidate = catalog(9, 0xaa)
        message = check_image_platforms(candidate, {
            "linux/amd64": catalog(9, 0xaa), "linux/arm64": catalog(9, 0xaa)})
        self.assertIn("canonical agent bytes", message)

    def test_one_drifted_platform_is_refused(self):
        candidate = catalog(9, 0xaa)
        with self.assertRaises(IdentityError) as caught:
            check_image_platforms(candidate, {
                "linux/amd64": catalog(9, 0xaa), "linux/arm64": catalog(9, 0xbb)})
        self.assertIn("linux/arm64", str(caught.exception))

    def test_a_platform_embedding_another_release_is_refused(self):
        with self.assertRaises(IdentityError) as caught:
            check_image_platforms(catalog(9, 0xaa), {
                "linux/amd64": catalog(9, 0xaa), "linux/arm64": catalog(8, 0xaa)})
        self.assertIn("release", str(caught.exception))

    # "We could not check" must never read as "it passed".
    def test_supplying_no_image_catalogs_is_refused(self):
        with self.assertRaises(IdentityError):
            check_image_platforms(catalog(9, 0xaa), {})

    # A non-empty check would accept amd64 alone — and arm64, built on a
    # different platform, is precisely the one that can drift.
    def test_a_single_platform_is_not_enough(self):
        with self.assertRaises(IdentityError) as caught:
            check_image_platforms(catalog(9, 0xaa), {"linux/amd64": catalog(9, 0xaa)})
        self.assertIn("linux/arm64", str(caught.exception))

    def test_an_unexpected_platform_is_refused(self):
        with self.assertRaises(IdentityError):
            check_image_platforms(catalog(9, 0xaa), {
                "linux/amd64": catalog(9, 0xaa), "linux/arm64": catalog(9, 0xaa),
                "darwin/arm64": catalog(9, 0xaa)})

    def test_identity_ignores_file_names_but_not_bytes(self):
        candidate = catalog(9, 0xaa)
        renamed = catalog(9, 0xaa)
        for platform in renamed["artifacts"]:
            renamed["artifacts"][platform]["file"] = "renamed-" + platform
        check_image_platforms(candidate, {"linux/amd64": renamed, "linux/arm64": renamed})

        resized = catalog(9, 0xaa)
        resized["artifacts"]["linux/amd64"]["size"] += 1
        with self.assertRaises(IdentityError):
            check_image_platforms(candidate, {"linux/amd64": resized, "linux/arm64": candidate})


class MalformedInputTests(unittest.TestCase):
    def broken(self, mutate):
        entry = catalog(9, 0xaa)
        mutate(entry)
        return entry

    def test_a_catalog_without_usable_identity_is_refused(self):
        def drop_platform(entry):
            del entry["artifacts"]["windows/amd64"]

        def bad_sha(entry):
            entry["artifacts"]["linux/amd64"]["sha256"] = "z" * 64  # right length, not hex

        def short_sha(entry):
            entry["artifacts"]["linux/amd64"]["sha256"] = "abc"

        def zero_size(entry):
            entry["artifacts"]["linux/amd64"]["size"] = 0

        def bool_size(entry):
            # bool is an int in Python, so True would sail through "size > 0".
            entry["artifacts"]["linux/amd64"]["size"] = True

        def bool_release(entry):
            entry["agent_release"] = True

        def zero_release(entry):
            entry["agent_release"] = 0

        for mutate in (drop_platform, bad_sha, short_sha, zero_size, bool_size,
                       bool_release, zero_release):
            with self.subTest(case=mutate.__name__), self.assertRaises(IdentityError):
                check_history(self.broken(mutate), history(**{"8": 0xbb}))


class CommandLineTests(unittest.TestCase):
    def run_gate(self, candidate, hist, images):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "candidate.json").write_text(json.dumps(candidate))
            (root / "history.json").write_text(json.dumps(hist))
            command = [sys.executable, str(Path(__file__).with_name("release_identity.py")),
                       "--candidate", str(root / "candidate.json"),
                       "--history", str(root / "history.json")]
            for platform, image in images.items():
                path = root / (platform.replace("/", "-") + ".json")
                path.write_text(json.dumps(image))
                command += ["--image-catalog", f"{platform}={path}"]
            return subprocess.run(command, capture_output=True, text=True)

    def test_a_good_release_exits_zero(self):
        result = self.run_gate(catalog(9, 0xaa), {"8": [artifacts(0xbb)]},
                               {"linux/amd64": catalog(9, 0xaa), "linux/arm64": catalog(9, 0xaa)})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Safe to promote", result.stdout)

    def test_a_conflicting_release_exits_nonzero(self):
        result = self.run_gate(catalog(8, 0xaa), {"8": [artifacts(0xbb)]},
                               {"linux/amd64": catalog(8, 0xaa), "linux/arm64": catalog(8, 0xaa)})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("release identity:", result.stderr)
        self.assertNotIn("Safe to promote", result.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
