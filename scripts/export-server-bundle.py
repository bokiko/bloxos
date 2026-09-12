#!/usr/bin/env python3
"""Package the exact published server images and updater into release assets."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipapp

sys.path.insert(0, str(Path(__file__).resolve().parent))
import agent_bundle  # noqa: E402  (same directory; the canonical payload checker)

AGENT_DIR = "agents"


def run(args):
    return subprocess.check_output(args, text=True).strip()


def platform_image(image, platform):
    """Resolve the immutable index to one child, avoiding classic-store collisions."""
    index = json.loads(run(["docker", "manifest", "inspect", image]))
    os_name, arch = platform.split("/")
    matches = []
    for item in index.get("manifests", []):
        target = item.get("platform", {})
        if target.get("os") == os_name and target.get("architecture") == arch:
            if target.get("variant", "") not in (("", "v8") if arch == "arm64" else ("",)):
                continue
            matches.append(item.get("digest", ""))
    if len(matches) != 1 or not re.fullmatch(r"sha256:[0-9a-f]{64}", matches[0]):
        raise ValueError("Published image index must contain exactly one supported " + platform + " manifest")
    return image.split("@", 1)[0] + "@" + matches[0]


def export(image, platform, source, destination, revision):
    image = platform_image(image, platform)
    subprocess.run(["docker", "pull", "--platform", platform, image], check=True)
    actual_platform = run(["docker", "image", "inspect", "--format", '{{.Os}}/{{.Architecture}}', image])
    if actual_platform != platform:
        raise ValueError("Published image platform does not match requested platform")
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


def stage_agents(source, tree, hub_image, version, revision):
    """Place the published agent payloads beside the hub binary they travel with.

    `agents/` is a DIRECT CHILD of the hub executable's own directory, which is
    what the hub looks for. The updater needs no change to carry it: it already
    extracts every member of the archive and copytree's the whole staged tree,
    and it installs the hub at <release>/hub/bloxos-hub — so <release>/hub/agents
    lands exactly where the resolver expects with no new manifest schema, no
    install logic and no second rollout step.

    The bytes are COPIED, never rebuilt. A rebuild here would produce a second
    set of agent binaries that are not the ones published as standalone assets,
    and the fleet would then be offered bytes nobody ever checked. So the source
    is the same directory whose files are attached to the release, and equality
    is asserted rather than assumed.
    """
    # The sidecar is itself a published release asset, so checking against it
    # ties the packed bytes to a value operators can independently see.
    digest = agent_bundle.read_regular(source / "agent-manifest.sha256", 4096).decode().split()[0]
    manifest = agent_bundle.check(source, digest)

    # Bind the bundle to THIS export. A valid bundle is not automatically the
    # RIGHT bundle: without this, any correctly-signed older bundle could be
    # packed beside a new hub, and the result would be a brand-new release that
    # once again offers the fleet stale agents — the precise failure this work
    # exists to end, reintroduced by the mechanism meant to fix it.
    #
    # export-agent-bundle.sh derives the catalog from one exact image, so in
    # this pipeline the catalog's identity must equal the export's. Referencing
    # an older immutable bundle on purpose is a different feature and would need
    # an explicit server-to-bundle reference, never silent acceptance of
    # whatever directory was passed in.
    expected = {"image_digest": hub_image.split("@", 1)[1], "source": revision, "version": version}
    mismatched = {key: (manifest.get(key), want) for key, want in expected.items() if manifest.get(key) != want}
    if mismatched:
        raise ValueError("Agent bundle does not belong to this server release: "
                         + "; ".join(f"{key} is {got!r}, expected {want!r}"
                                     for key, (got, want) in sorted(mismatched.items())))

    destination = tree / "hub" / AGENT_DIR
    destination.mkdir()
    names = [agent_bundle.MANIFEST, *agent_bundle.FILES.values()]
    for name in names:
        data = agent_bundle.read_regular(source / name,
                                         64 * 1024 if name == agent_bundle.MANIFEST else agent_bundle.MAX_BINARY)
        with (destination / name).open("xb") as stream:
            stream.write(data)
        # Assert equality against the source bytes, per file. A copy that
        # silently truncated or substituted would otherwise be discovered by
        # the fleet rather than by the release build.
        if (destination / name).read_bytes() != data:
            raise ValueError("Packed agent payload does not match the published bytes: " + name)
        (destination / name).chmod(0o644 if name == agent_bundle.MANIFEST else 0o755)

    # Re-run the full check on the DESTINATION: manifest SHA, per-payload
    # sha256/size, architecture read from each image header, and a single
    # consistent release marker across all three platforms.
    if agent_bundle.check(destination, digest) != manifest:
        raise ValueError("Packed agent bundle does not match the published agent bundle")
    return manifest


def verify_packed_agents(archive, manifest):
    """Prove the payload BYTES survived into the archive, at the path the hub reads.

    Checking member names would only prove something with the right name is
    present. The archive is the artifact that actually ships, so the bytes
    inside it are what has to be hashed — a truncated, substituted or aliased
    entry passes a name check and fails the fleet.

    The PAYLOADS are compared byte for byte, via sha256 and size against the
    catalog. The CATALOG itself is compared as parsed JSON, so re-serialisation
    whitespace is tolerated: the manifest's own integrity is already pinned by
    its sidecar SHA in stage_agents, and there is no security value in failing
    a release over a reformatted but semantically identical document.
    """
    prefix = "hub/" + AGENT_DIR + "/"
    required = {agent_bundle.MANIFEST, *agent_bundle.FILES.values()}
    found = {}
    with tarfile.open(archive, "r:gz") as target:
        for member in target.getmembers():
            if not member.name.startswith(prefix):
                continue
            name = member.name[len(prefix):]
            if "/" in name:
                raise ValueError("Agent bundle directory has unexpected nesting: " + member.name)
            # A hardlink or symlink entry resolves to bytes chosen elsewhere in
            # the archive, so it is never an acceptable stand-in for a payload.
            if not member.isfile() or member.issym() or member.islnk():
                raise ValueError("Agent bundle entry is not a regular file: " + member.name)
            if name in found:
                raise ValueError("Agent bundle contains a duplicate entry: " + member.name)
            if name not in required:
                raise ValueError("Agent bundle contains an unexpected file: " + member.name)
            stream = target.extractfile(member)
            if stream is None:
                raise ValueError("Agent bundle entry has no content: " + member.name)
            found[name] = stream.read()

    missing = sorted(required - set(found))
    if missing:
        raise ValueError("Server archive is missing agent payloads: " + ", ".join(missing))

    # The archived catalog must be the same catalog that was verified — not
    # merely a parseable manifest that happens to be present.
    if json.loads(found[agent_bundle.MANIFEST]) != manifest:
        raise ValueError("Archived agent catalog differs from the verified catalog")

    # Every server architecture carries every agent platform, so a hub on either
    # server can serve any machine in the fleet.
    for platform, name in agent_bundle.FILES.items():
        artifact = manifest["artifacts"].get(platform)
        if artifact is None:
            raise ValueError("Packed agent bundle does not declare " + platform)
        if artifact["file"] != name:
            raise ValueError("Packed agent bundle renames " + platform)
        if len(found[name]) != artifact["size"] or \
                hashlib.sha256(found[name]).hexdigest() != artifact["sha256"]:
            raise ValueError("Archived agent payload does not match the catalog: " + name)


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
    # Required, not optional: a server archive without agents is the exact
    # failure this work exists to end — a new hub that keeps offering the fleet
    # whatever agent binaries happen to already be on disk.
    parser.add_argument("--agents", type=Path, required=True,
                        help="directory of published agent payloads from export-agent-bundle.sh")
    args = parser.parse_args()
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", args.version) or not re.fullmatch(r"[0-9a-f]{40}", args.revision):
        parser.error("Expected release tag and full revision")
    for name in ("hub", "dashboard"):
        if not re.fullmatch(r"ghcr\.io/bokiko/bloxos-" + name + r"@sha256:[0-9a-f]{64}", getattr(args, name)):
            parser.error("Image references must be canonical digest-pinned BloxOS images")
    args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
    # No schema bump. The updater verifies the archive checksum and extracts
    # safely already; adding agent payloads inside the archive needs nothing new
    # from it, and a schema change would force old workers to be upgraded first.
    manifest = {"schema": 1, "version": args.version, "revision": args.revision,
                "images": {"hub": args.hub, "dashboard": args.dashboard}, "native": {}}
    for arch in ("amd64", "arm64"):
        with tempfile.TemporaryDirectory(prefix="bloxos-server-export-") as directory:
            tree = Path(directory)
            (tree / "hub").mkdir()
            (tree / "dashboard").mkdir()
            export(args.hub, "linux/" + arch, "/usr/local/bin/bloxos-hub", tree / "hub" / "bloxos-hub", args.revision)
            agents = stage_agents(args.agents, tree, args.hub, args.version, args.revision)
            export(args.dashboard, "linux/" + arch, "/app/.", tree / "dashboard", args.revision)
            if not (tree / "dashboard" / "server.js").is_file():
                raise ValueError("Exported dashboard has no standalone server")
            if not (tree / "dashboard" / ".next").is_dir():
                raise ValueError("Exported dashboard has no compiled .next assets")
            name = f"bloxos-server-linux-{arch}.tar.gz"
            archive = args.output / name
            pack_tree(tree, archive)
            verify_packed_agents(archive, agents)
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
