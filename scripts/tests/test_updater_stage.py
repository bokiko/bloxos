import os
from pathlib import Path
import tempfile
import unittest

from test_updater_native import native_config, adapter_with_bundle, Runner


class StageSafetyTests(unittest.TestCase):
    def test_repeated_release_never_removes_running_tree(self):
        with tempfile.TemporaryDirectory() as temporary:
            config = native_config(temporary)
            adapter, manifest = adapter_with_bundle(temporary, config, Runner())
            running = Path(adapter._release_dir(manifest))
            running.mkdir(parents=True)
            (running / "live-marker").write_text("keep")
            adapter.stage(manifest, str(Path(temporary) / "first"))
            first = adapter.journal.get("release_dir")
            adapter.stage(manifest, str(Path(temporary) / "second"))
            second = adapter.journal.get("release_dir")
            self.assertNotEqual(first, second)
            self.assertTrue(Path(first).is_dir())
            self.assertEqual((running / "live-marker").read_text(), "keep")

    def test_private_worker_umask_does_not_hide_release_code(self):
        with tempfile.TemporaryDirectory() as temporary:
            config = native_config(temporary)
            adapter, manifest = adapter_with_bundle(temporary, config, Runner())
            previous = os.umask(0o077)
            try:
                adapter.stage(manifest, str(Path(temporary) / "stage"))
            finally:
                os.umask(previous)
            release = Path(adapter.journal.get("release_dir"))
            self.assertEqual(release.parent.stat().st_mode & 0o777, 0o755)
            self.assertEqual(release.stat().st_mode & 0o777, 0o755)


if __name__ == "__main__":
    unittest.main()
