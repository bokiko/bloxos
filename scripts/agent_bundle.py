#!/usr/bin/env python3
"""Inspect/stage native agent payloads. Never install, sign, execute or announce them.

Use a manifest SHA obtained through the trusted release handoff. A matching
checksum is integrity evidence, not a replacement for the fleet's update key.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import struct
import tempfile


FILES = {
    "linux/amd64": "bloxos-agent-linux-amd64",
    "linux/arm64": "bloxos-agent-linux-arm64",
    "windows/amd64": "bloxos-agent-windows-amd64.exe",
}
MANIFEST = "agent-manifest.json"
MAX_BINARY = 128 * 1024 * 1024
HEX = re.compile(r"[0-9a-f]{64}\Z")
MARKER = re.compile(rb"BLOXOS-AGENT-RELEASE:([0-9]{10}):")
VERSION = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?\Z")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def validate_tag(version):
    require(isinstance(version, str) and VERSION.fullmatch(version) and len(version) <= 129,
            "expected vX.Y.Z or vX.Y.Z-prerelease (Docker-compatible, no build metadata)")
    return version


def read_regular(path, limit):
    """No symlinks/FIFOs; read the descriptor we checked, not a second pathname."""
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode), f"not a regular file: {path}")
        require(0 < info.st_size <= limit, f"invalid file size: {path}")
        data = stream.read(limit + 1)
        require(len(data) == info.st_size, f"file changed while reading: {path}")
        return data


def identity(data, platform):
    require(len(data) >= 64, f"truncated binary: {platform}")
    if platform.startswith("linux/"):
        # Byte 6 is EI_VERSION. The hub's loader requires EV_CURRENT, so this
        # must too: a validator that accepts what the consumer rejects lets a
        # release pass packaging and then fail at hub startup, which is the
        # worst possible place to find out.
        require(data[:7] == b"\x7fELF\x02\x01\x01", f"expected 64-bit little-endian ELF: {platform}")
        machine = struct.unpack_from("<H", data, 18)[0]
        require(machine == {"linux/amd64": 62, "linux/arm64": 183}[platform],
                f"wrong ELF architecture: {platform}")
    else:
        require(data[:2] == b"MZ", "expected Windows PE executable")
        offset = struct.unpack_from("<I", data, 60)[0]
        # Below the DOS header the offset points back into the stub.
        require(64 <= offset and offset + 26 <= len(data) and data[offset:offset + 4] == b"PE\0\0",
                "invalid PE header")
        require(struct.unpack_from("<H", data, offset + 4)[0] == 0x8664
                and struct.unpack_from("<H", data, offset + 24)[0] == 0x20B,
                "expected Windows amd64 PE32+")
    releases = [int(m) for m in MARKER.findall(data)]
    require(data.count(b"BLOXOS-AGENT-RELEASE:") == 1 and len(releases) == 1 and releases[0] > 0,
            f"missing, zero or conflicting release markers: {platform}")
    return releases[0]


def inspect_payloads(directory):
    artifacts = {}
    releases = set()
    for platform, name in FILES.items():
        data = read_regular(directory / name, MAX_BINARY)
        releases.add(identity(data, platform))
        artifacts[platform] = {"file": name, "size": len(data),
                               "sha256": hashlib.sha256(data).hexdigest()}
    require(len(releases) == 1, "platforms carry different agent release numbers")
    return next(iter(releases)), artifacts


# Provenance fields and how each is spelled when it IS present. The catalog
# built inside the hub image cannot carry all of them — the image digest does
# not exist until the image it would describe has been built, and a plain local
# `docker build` has no release tag or git SHA either.
#
# Unavailable provenance is OMITTED, never faked. A placeholder all-zero SHA or
# a self-referential digest would be a field that looks like evidence and is
# not, and every later check against it would pass while proving nothing.
PROVENANCE = {
    "source": (lambda v: re.fullmatch(r"[0-9a-f]{40}", v), "expected full source commit SHA"),
    "version": (lambda v: VERSION.fullmatch(v) and len(v) <= 129, "expected vX.Y.Z or vX.Y.Z-prerelease"),
    "image_digest": (lambda v: re.fullmatch(r"sha256:[0-9a-f]{64}", v), "expected image digest"),
}
# Placeholders the Dockerfile defaults to. They mean "not known", so they are
# treated as absent rather than recorded as fact.
UNKNOWN = {"unknown", "development", "", None}


def provenance_of(values):
    """Validate every provenance value that is present; drop the unknown ones."""
    present = {}
    for field, value in values.items():
        if value in UNKNOWN:
            continue
        check_value, message = PROVENANCE[field]
        require(isinstance(value, str) and check_value(value), f"{message}: {field}")
        present[field] = value
    return present


def create_manifest(directory, source, version, image_digest):
    release, artifacts = inspect_payloads(directory)
    manifest = {"schema": 1, "agent_release": release, "artifacts": artifacts,
                **provenance_of({"source": source, "version": version, "image_digest": image_digest})}
    data = (json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode()
    # The release builder refuses to overwrite a previous manifest.
    with (directory / MANIFEST).open("xb") as stream:
        stream.write(data)
    return hashlib.sha256(data).hexdigest()


def check(directory, manifest_sha, require_provenance=True):
    """Verify a catalog and its payloads.

    require_provenance=False is for the catalog built INSIDE the hub image,
    which legitimately cannot name the image it lives in. It relaxes exactly
    one thing: whether a provenance field must be PRESENT. Any field that IS
    present is still validated strictly, and the artifact checks — sha256,
    size, architecture, a single consistent release marker, all three platforms
    — are mandatory either way. Those are what decide which bytes the fleet is
    offered; provenance only says where they came from.
    """
    require(HEX.fullmatch(manifest_sha), "supply the trusted 64-character manifest SHA256")
    data = read_regular(directory / MANIFEST, 64 * 1024)
    require(hashlib.sha256(data).hexdigest() == manifest_sha, "manifest SHA256 mismatch")
    manifest = json.loads(data)
    require(isinstance(manifest, dict) and manifest.get("schema") == 1, "unsupported manifest")
    for field, (check_value, message) in PROVENANCE.items():
        value = manifest.get(field)
        if value is None:
            require(not require_provenance, f"release catalog is missing {field}")
            continue
        require(isinstance(value, str) and check_value(value), f"invalid {field}: {message}")
    release, artifacts = inspect_payloads(directory)
    require(type(manifest.get("agent_release")) is int and manifest["agent_release"] == release,
            "manifest/binary release mismatch")
    require(manifest.get("artifacts") == artifacts, "artifact SHA256, size, filename or architecture mismatch")
    return manifest


def safe_staging_root(root):
    require(root.is_dir() and not root.is_symlink(), "staging root must be an existing real directory")
    resolved = root.resolve()
    info = resolved.stat()
    require(info.st_uid == os.geteuid() and not info.st_mode & 0o022,
            "staging root must be owned by the invoking user and not writable by group/others")
    forbidden = [Path("/usr/local/lib/bloxos/linux"), Path("/usr/local/lib/bloxos/windows")]
    for name in ("BLOXOS_AGENT_BINARY", "BLOXOS_AGENT_BINARY_ARM64", "BLOXOS_AGENT_BINARY_WINDOWS"):
        # Match Go strings.TrimSpace in the hub resolver (Unicode White_Space,
        # unlike Python's extra U+001C..U+001F separators). Relative overrides
        # are rejected by the hub; never interpret them against our own cwd.
        value = os.environ.get(name, "").strip(
            "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004"
            "\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000")
        if value:
            require(Path(value).is_absolute(), f"{name} must be an absolute path")
            forbidden.append(Path(value).resolve().parent)
    for directory in forbidden:
        directory = directory.resolve()
        require(resolved != directory and directory not in resolved.parents
                and resolved not in directory.parents,
                "staging root overlaps an active agent directory")
    require(resolved not in (Path("/"), Path.home()), "choose a dedicated staging directory")
    return resolved


def stage(directory, manifest_sha, root):
    manifest = check(directory, manifest_sha)
    root = safe_staging_root(root)
    destination = root / f"agent-release-{manifest['agent_release']}"
    # Reuse of a release number cannot replace a different staged payload.
    require(not destination.exists() and not destination.is_symlink(),
            f"already staged: {destination}; use check, never overwrite a release")
    temporary = Path(tempfile.mkdtemp(prefix=".agent-stage-", dir=root))
    try:
        for name in [MANIFEST, *FILES.values()]:
            data = read_regular(directory / name, 64 * 1024 if name == MANIFEST else MAX_BINARY)
            with (temporary / name).open("xb") as stream:
                stream.write(data)
            (temporary / name).chmod(0o444)
        # Recheck the copied bytes, catching source changes between check/copy.
        check(temporary, manifest_sha)
        # Never replace even an empty directory created by another operator.
        destination.mkdir(mode=0o700)
        try:
            for name in [MANIFEST, *FILES.values()]:
                (temporary / name).rename(destination / name)
            destination.chmod(0o555)
        except Exception:
            for name in [MANIFEST, *FILES.values()]:
                (destination / name).unlink(missing_ok=True)
            destination.rmdir()
            raise
    finally:
        # Only our unique temporary directory, never an operator's existing tree.
        shutil.rmtree(temporary)
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    p = commands.add_parser("validate-tag", help="release-builder preflight; no files or network")
    p.add_argument("--version", required=True)
    for name in ("check", "stage", "manifest"):
        p = commands.add_parser(name)
        p.add_argument("--bundle", required=True, type=Path)
        if name == "manifest":
            # Not required: an in-image build catalog omits what it cannot know.
            p.add_argument("--source", default=None)
            p.add_argument("--version", default=None)
            p.add_argument("--image-digest", default=None)
        else:
            p.add_argument("--manifest-sha256", required=True)
            p.add_argument("--allow-missing-provenance", action="store_true",
                           help="for the catalog built inside the hub image")
        if name == "stage":
            p.add_argument("--staging-root", required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.command == "validate-tag":
            print(validate_tag(args.version))
        elif args.command == "manifest":
            print(create_manifest(args.bundle, args.source, args.version, args.image_digest))
        elif args.command == "check":
            print(json.dumps(check(args.bundle, args.manifest_sha256,
                                   require_provenance=not args.allow_missing_provenance), indent=2))
            print("Payloads match the supplied manifest. Fleet signatures must be verified separately. No changes made.")
        else:
            print(f"Staged at {stage(args.bundle, args.manifest_sha256, args.staging_root)}")
            print("No active binary changed. Nothing signed, restarted or announced. Activation is separate.")
    except (OSError, ValueError, TypeError) as error:
        parser.exit(1, f"agent-bundle: {error}\n")


if __name__ == "__main__":
    main()
