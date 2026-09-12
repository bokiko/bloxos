#!/usr/bin/env python3
"""Offline tests for the release history gatherer. Releases and assets injected."""
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from release_history import CATALOG_ASSET, HistoryError, catalog_of, gather, persist, upload

PLATFORMS = ("linux/amd64", "linux/arm64", "windows/amd64")


def artifacts(seed):
    return {platform: {"file": platform, "size": 100 + index,
                       "sha256": f"{seed:02x}{index:02x}" + "0" * 60}
            for index, platform in enumerate(PLATFORMS)}


def catalog(release, seed):
    return {"schema": 1, "agent_release": release, "artifacts": artifacts(seed)}


def release_entry(tag, asset_id=None, draft=False):
    assets = [] if asset_id is None else [{"name": CATALOG_ASSET, "id": asset_id}]
    # Releases carry other assets too; the catalog must be found among them.
    assets.append({"name": "bloxos-update", "id": 9000})
    return {"tag": tag, "draft": draft, "assets": assets}


def fetcher(bodies):
    def fetch(repo, asset_id):
        if asset_id not in bodies:
            raise HistoryError(f"asset {asset_id} could not be downloaded")
        return json.dumps(bodies[asset_id])
    return fetch


def fetcher_bytes(bodies):
    """A fetcher that returns RAW BYTES, as the real one does."""
    def fetch(repo, asset_id):
        if asset_id not in bodies:
            raise HistoryError(f"asset {asset_id} could not be downloaded")
        return bodies[asset_id]
    return fetch


class GatherTests(unittest.TestCase):
    def test_every_catalog_is_retained_per_release_number(self):
        releases = [release_entry("v1.7.0", 1), release_entry("v1.7.1", 2)]
        history = gather("owner/repo", releases, fetcher({1: catalog(7, 0xaa), 2: catalog(7, 0xbb)}))
        self.assertEqual(len(history["7"]), 2,
                         "both catalogs must survive; collapsing them destroys the divergence "
                         "the identity gate exists to find")

    def test_drafts_are_included(self):
        releases = [release_entry("v2.0.0", 1, draft=True)]
        history = gather("owner/repo", releases, fetcher({1: catalog(9, 0xaa)}))
        self.assertIn("9", history,
                      "tags are promoted before draft assets attach, so a draft release can "
                      "already be serving agents")

    # The only path that may contribute nothing without failing.
    def test_a_release_with_no_catalog_asset_is_genuinely_absent(self):
        releases = [release_entry("v1.0.0", None), release_entry("v1.7.1", 2)]
        history = gather("owner/repo", releases, fetcher({2: catalog(7, 0xaa)}))
        self.assertEqual(sorted(history), ["7"])

    # The failure this test guards is the dangerous one: a timeout on the
    # NEWEST release silently erases the floor, and the older history that did
    # load lets a stale candidate pass a check that reported success.
    def test_a_download_failure_is_fatal_not_absence(self):
        releases = [release_entry("v1.7.1", 1), release_entry("v2.0.0", 2)]
        with self.assertRaises(HistoryError) as caught:
            gather("owner/repo", releases, fetcher({1: catalog(7, 0xaa)}))  # 2 unavailable
        self.assertIn("could not be downloaded", str(caught.exception))

    def test_a_missing_asset_list_is_fatal(self):
        with self.assertRaises(HistoryError):
            gather("owner/repo", [{"tag": "v1.0.0", "draft": False}], fetcher({}))

    def test_an_unparseable_catalog_is_fatal(self):
        def fetch(repo, asset_id):
            return "{not json"
        with self.assertRaises(HistoryError) as caught:
            gather("owner/repo", [release_entry("v1.0.0", 1)], fetch)
        self.assertIn("unreadable", str(caught.exception))

    def test_a_malformed_catalog_is_fatal(self):
        for broken in ({"agent_release": "seven", "artifacts": artifacts(1)},
                       {"agent_release": True, "artifacts": artifacts(1)},
                       {"agent_release": 7, "artifacts": "not a map"},
                       {"artifacts": artifacts(1)}):
            with self.subTest(catalog=broken), self.assertRaises(HistoryError):
                gather("owner/repo", [release_entry("v1.0.0", 1)], fetcher({1: broken}))

    # A retry runs after a previous run may already have persisted and promoted
    # this very tag, so its catalog IS history.
    def test_the_candidate_tags_own_catalog_is_included(self):
        releases = [release_entry("v2.0.0", 1)]
        history = gather("owner/repo", releases, fetcher({1: catalog(9, 0xaa)}))
        self.assertIn("9", history)

    def test_pagination_keeps_every_page(self):
        # The gatherer is handed the full listing; this proves nothing is
        # dropped when that listing spans many releases, which is what the
        # removed --limit 200 would eventually have truncated.
        releases, bodies = [], {}
        for index in range(250):
            releases.append(release_entry(f"v1.0.{index}", index + 1))
            bodies[index + 1] = catalog(index + 1, index % 256)
        history = gather("owner/repo", releases, fetcher(bodies))
        self.assertEqual(len(history), 250)
        self.assertIn("250", history)


class CatalogLookupTests(unittest.TestCase):
    def test_the_catalog_is_found_among_other_assets(self):
        entry = release_entry("v1.0.0", 5)
        self.assertEqual(catalog_of("owner/repo", entry, fetcher({5: catalog(3, 0x01)}))["agent_release"], 3)

    def test_an_absent_catalog_returns_none(self):
        self.assertIsNone(catalog_of("owner/repo", release_entry("v1.0.0", None), fetcher({})))


class PersistTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / CATALOG_ASSET
        self.path.write_text(json.dumps(catalog(9, 0xaa)))

    def test_an_identical_existing_catalog_is_a_no_op(self):
        releases = [release_entry("v2.0.0", 1)]
        message = persist("owner/repo", "v2.0.0", self.path, releases, fetcher({1: catalog(9, 0xaa)}))
        self.assertIn("nothing to do", message)

    def test_a_conflicting_existing_catalog_is_never_overwritten(self):
        releases = [release_entry("v2.0.0", 1)]
        with self.assertRaises(HistoryError) as caught:
            persist("owner/repo", "v2.0.0", self.path, releases, fetcher({1: catalog(9, 0xbb)}))
        self.assertIn("never", str(caught.exception))

    def test_a_download_failure_does_not_become_permission_to_upload(self):
        releases = [release_entry("v2.0.0", 1)]
        with self.assertRaises(HistoryError):
            persist("owner/repo", "v2.0.0", self.path, releases, fetcher({}))


class BinaryAssetTests(unittest.TestCase):
    """Release assets are agent binaries and tarballs, not text.

    Reading them through a text-mode pipe decodes arbitrary bytes as UTF-8 and
    rewrites line endings, so a retry comparing an existing asset would either
    raise a decode error or compare data the transport had already altered and
    report a conflict that does not exist.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)

    def write(self, name, body):
        path = Path(self.temp.name) / name
        path.write_bytes(body)
        return path

    # A real ELF header, a NUL run, a lone 0x80 continuation byte (invalid
    # UTF-8), and a CRLF that text mode would rewrite.
    BINARY = b"\x7fELF\x02\x01\x01\x00" + b"\x00" * 8 + b"\x80\xfe\xff" + b"line\r\nline"

    def test_an_identical_binary_asset_is_skipped_on_retry(self):
        path = self.write("bloxos-agent-linux-amd64", self.BINARY)
        releases = [release_entry("v2.0.0", None)]
        releases[0]["assets"].append({"name": path.name, "id": 42})

        results = upload("owner/repo", "v2.0.0", [path], releases,
                         fetcher_bytes({42: self.BINARY}))
        self.assertEqual(results, [f"{path.name}: already published and identical, skipped"])

    def test_a_differing_binary_asset_is_never_overwritten(self):
        path = self.write("bloxos-agent-linux-amd64", self.BINARY)
        releases = [release_entry("v2.0.0", None)]
        releases[0]["assets"].append({"name": path.name, "id": 42})

        with self.assertRaises(HistoryError) as caught:
            upload("owner/repo", "v2.0.0", [path], releases,
                   fetcher_bytes({42: self.BINARY + b"\x01"}))
        self.assertIn("never", str(caught.exception))

    def test_catalog_persistence_compares_exact_bytes(self):
        """Semantically identical JSON with different formatting is NOT the same
        asset: agent-manifest.sha256 covers the catalog's exact bytes."""
        body = json.dumps(catalog(9, 0xaa), indent=2).encode()
        path = self.write(CATALOG_ASSET, body)
        releases = [release_entry("v2.0.0", 1)]

        # Byte-identical: a no-op.
        self.assertIn("nothing to do",
                      persist("owner/repo", "v2.0.0", path, releases, fetcher_bytes({1: body})))

        # Same object, different serialisation: refused, because the published
        # sidecar checksum names those other bytes.
        reformatted = json.dumps(catalog(9, 0xaa), separators=(",", ":")).encode()
        self.assertNotEqual(reformatted, body)
        with self.assertRaises(HistoryError):
            persist("owner/repo", "v2.0.0", path, releases, fetcher_bytes({1: reformatted}))


if __name__ == "__main__":
    unittest.main(verbosity=2)
