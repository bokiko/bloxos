#!/usr/bin/env python3
"""Export local test images for native smoke tests; never a release publisher."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile


def main():
    if os.environ.get("SMOKE_CONFIRM_DISPOSABLE") != "1":
        raise SystemExit("Only for a disposable smoke-test host")
    spec = importlib.util.spec_from_file_location("bundle", Path(__file__).resolve().parents[1] / "export-server-bundle.py")
    bundle = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(bundle)
    output = Path("/tmp/bloxos-updater-native-bundles")
    output.mkdir(mode=0o755, exist_ok=False)
    arch = "arm64" if platform.machine() in ("arm64", "aarch64") else "amd64"
    for kind in ("original", "candidate"):
        destination = output / kind
        destination.mkdir()
        with tempfile.TemporaryDirectory() as temporary:
            tree = Path(temporary)
            for name in ("hub", "dashboard"):
                (tree / name).mkdir()
                image = f"ghcr.io/bokiko/bloxos-{name}:1.2.2" if kind == "original" else f"bloxos-updater-test-{name}"
                container = subprocess.check_output(["docker", "create", image], text=True).strip()
                source = "/usr/local/bin/bloxos-hub" if name == "hub" else "/app/."
                target = tree / "hub/bloxos-hub" if name == "hub" else tree / "dashboard"
                try:
                    subprocess.run(["docker", "cp", container + ":" + source, str(target)], check=True)
                finally:
                    subprocess.run(["docker", "rm", "-v", container], check=True, stdout=subprocess.DEVNULL)
            filename = f"bloxos-server-linux-{arch}.tar.gz"
            archive = destination / filename
            bundle.pack_tree(tree, archive)
            manifest = {"schema": 1, "version": "v1.2.2" if kind == "original" else "v1.3.0",
                        "revision": "4b96ad6564c24b089b30b41ee74557fd03e9aaa7", "images": {},
                        "native": {arch: {"file": filename, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest()}}}
            (destination / "update-manifest.json").write_text(json.dumps(manifest))
    print("Native fixtures exported to", output)


if __name__ == "__main__":
    main()
