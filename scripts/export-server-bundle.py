#!/usr/bin/env python3
"""Package the exact published server images and updater into release assets."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipapp


def run(args):
    return subprocess.check_output(args, text=True).strip()


def export(image, platform, source, destination, revision):
    subprocess.run(["docker", "pull", "--platform", platform, image], check=True)
    actual = run(["docker", "image", "inspect", "--format", '{{index .Config.Labels "org.opencontainers.image.revision"}}', image])
    if actual != revision:
        raise ValueError("Published image revision does not match release")
    container = run(["docker", "create", "--platform", platform, "--network", "none", image])
    if not re.fullmatch(r"[0-9a-f]{64}", container):
        raise ValueError("Invalid export container ID")
    try:
        subprocess.run(["docker", "cp", container + ":" + source, str(destination)], check=True)
    finally:
        subprocess.run(["docker", "rm", "-v", container], check=True, stdout=subprocess.DEVNULL)


def pack_tree(tree, archive):
    root = tree.resolve()
    for entry in tree.rglob("*"):
        if entry.is_symlink() and not entry.resolve().is_relative_to(root):
            raise ValueError("Bundle symlink escapes exported server tree")
    # Node resolves modules from their REAL path. Dereferencing pnpm links
    # changes that path and breaks dependency lookup. Preserve validated local
    # relative links, including links to packages pruned by Next's tracer.
    with tarfile.open(archive, "w:gz", dereference=False) as target:
        def safe_member(member):
            if member.islnk():
                member.type = tarfile.REGTYPE
                member.linkname = ""
                member.size = (tree / member.name).stat().st_size
            if member.issym():
                if Path(member.linkname).is_absolute():
                    raise ValueError("Bundle contains an absolute symlink")
            elif not (member.isfile() or member.isdir()):
                raise ValueError("Bundle contains a non-regular filesystem entry")
            return member
        for directory in ("hub", "dashboard"):
            target.add(tree / directory, arcname=directory, filter=safe_member)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--hub", required=True)
    parser.add_argument("--dashboard", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", args.version) or not re.fullmatch(r"[0-9a-f]{40}", args.revision):
        parser.error("Expected release tag and full revision")
    for name in ("hub", "dashboard"):
        if not re.fullmatch(r"ghcr\.io/bokiko/bloxos-" + name + r"@sha256:[0-9a-f]{64}", getattr(args, name)):
            parser.error("Image references must be canonical digest-pinned BloxOS images")
    args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
    manifest = {"schema": 1, "version": args.version, "revision": args.revision,
                "images": {"hub": args.hub, "dashboard": args.dashboard}, "native": {}}
    for arch in ("amd64", "arm64"):
        with tempfile.TemporaryDirectory(prefix="bloxos-server-export-") as directory:
            tree = Path(directory)
            (tree / "hub").mkdir()
            (tree / "dashboard").mkdir()
            export(args.hub, "linux/" + arch, "/usr/local/bin/bloxos-hub", tree / "hub" / "bloxos-hub", args.revision)
            export(args.dashboard, "linux/" + arch, "/app/.", tree / "dashboard", args.revision)
            if not (tree / "dashboard" / "server.js").is_file():
                raise ValueError("Exported dashboard has no standalone server")
            if not (tree / "dashboard" / ".next").is_dir():
                raise ValueError("Exported dashboard has no compiled .next assets")
            name = f"bloxos-server-linux-{arch}.tar.gz"
            archive = args.output / name
            pack_tree(tree, archive)
            manifest["native"][arch] = {"file": name, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest()}
    (args.output / "update-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    with tempfile.TemporaryDirectory(prefix="bloxos-updater-release-") as directory:
        package = Path(directory) / "updater"
        package.mkdir()
        for source in (Path(__file__).parent / "updater").glob("*.py"):
            shutil.copyfile(source, package / source.name)
        zipapp.create_archive(directory, args.output / "bloxos-update", interpreter="/usr/bin/python3", main="updater.cli:entrypoint")
    subprocess.run(["python3", str(args.output / "bloxos-update"), "--help"], check=True)


if __name__ == "__main__":
    main()
