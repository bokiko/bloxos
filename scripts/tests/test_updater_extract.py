import io
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater.engine import safe_extract_tar, UpdaterError


class ExtractTests(unittest.TestCase):
    def unpack(self, root, entries):
        archive = root / "test.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            for name, kind, value in entries:
                member = tarfile.TarInfo(name)
                member.mode = 0o6755
                if kind == "link":
                    member.type, member.linkname = tarfile.SYMTYPE, value
                    tar.addfile(member)
                else:
                    member.size = len(value)
                    tar.addfile(member, io.BytesIO(value))
        safe_extract_tar(str(archive), str(root / "out"))

    def test_internal_and_dangling_links_preserved_without_setuid(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.unpack(root, [("pkg/module.js", "file", b"ok"), ("module", "link", "pkg/module.js"), ("pruned", "link", "pkg/missing")])
            self.assertEqual((root / "out/module").read_bytes(), b"ok")
            self.assertTrue((root / "out/pruned").is_symlink())
            self.assertEqual((root / "out/pkg/module.js").stat().st_mode & 0o6000, 0)

    def test_escape_and_cycles_are_rejected(self):
        cases = [
            [("a", "link", "../outside")],
            [("a", "link", "/etc/passwd")],
            [("a", "link", "b"), ("b", "link", "a")],
            [("dir/a", "link", ".."), ("escape", "link", "dir/a/../outside")],
            [("a", "link", "target"), ("a/file", "file", b"bad")],
        ]
        for entries in cases:
            with self.subTest(entries=entries), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                with self.assertRaises(UpdaterError):
                    self.unpack(root, entries)
                self.assertFalse((root / "outside").exists())

    def test_existing_directory_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "out").mkdir()
            (root / "out/user-file").write_text("keep")
            with self.assertRaisesRegex(UpdaterError, "empty"):
                self.unpack(root, [("file", "file", b"data")])
            self.assertEqual((root / "out/user-file").read_text(), "keep")


if __name__ == "__main__":
    unittest.main()
