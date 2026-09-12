#!/usr/bin/env python3
"""Gather published agent release history, and persist a catalog immutably.

Two jobs, both about the same thing: what a release number has already meant.

`gather` collects every agent catalog this repository has ever published, so
the identity gate can compare a candidate against all of them. It includes
DRAFT releases deliberately. The release workflow promotes Docker tags before
it attaches draft assets, so a release whose images are already live and whose
agents are already being served can still be a draft — treating drafts as
absent would read such a release as never having happened. It also includes the
candidate tag's OWN catalog, because a previous run may already have persisted
and promoted it; "its own catalog is not history" is false the moment a retry
exists.

FAILURE IS NEVER ABSENCE. The only reason a release contributes nothing is that
its asset list positively does not contain a catalog. A listing error, a
download error, an auth failure or a parse error is fatal. The alternative is
far worse than a failed build: a timeout while fetching the NEWEST release
would silently erase the floor, and the older history that still loaded would
let a stale or conflicting candidate sail through a check that reported success.

`persist` writes the verified catalog to the draft release BEFORE the Docker
tags are promoted. If a run dies between promotion and asset upload, the images
are live but nothing records what agent bytes they carry, and the next run has
no history to check itself against. Persisting first means a crash can leave an
unused catalog — harmless — instead of unrecorded published bytes.

Persistence is idempotent and never destructive: re-uploading an identical
catalog is a no-op, and a DIFFERENT catalog under the same tag is an error
rather than an overwrite. An asset that has already been published is evidence,
and evidence does not get replaced because a later run disagreed with it.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys

CATALOG_ASSET = "agent-manifest.json"

# The published image pair this release was built from.
#
# A publish retry rebuilds both images, and nothing guarantees a rebuild lands
# on the same index digest — it MAY differ, which is enough. When it does, the
# retry promotes tags pointing at a new digest while the catalog persisted by
# the first attempt describes the old one, and every downstream check that ties
# a bundle to its image fails permanently, with no way to finish the release
# because a published catalog is never overwritten. So a retry reuses the pair
# the first attempt verified rather than depending on a rebuild matching it.
#
# This is a CI-only record. The updater's manifest schema is untouched; no
# worker reads this file and none needs to.
BUILD_ASSET = "release-build.json"
BUILD_SCHEMA = 1
IMAGE_REF = re.compile(r"ghcr\.io/bokiko/bloxos-(hub|dashboard)@sha256:[0-9a-f]{64}\Z")


class HistoryError(Exception):
    """A lookup that did not complete. Never treated as 'nothing found'."""


def gh(*args):
    """Run gh and return TEXT stdout. For listings and commands, never assets."""
    result = subprocess.run(["gh", *args], text=True, capture_output=True)
    if result.returncode != 0:
        raise HistoryError(f"gh {' '.join(args[:3])} failed: {result.stderr.strip()}")
    return result.stdout


def gh_bytes(*args):
    """Run gh and return stdout as RAW BYTES, with stderr decoded only for the error.

    Release assets are agent binaries and tarballs. Reading them through a
    text-mode pipe decodes arbitrary bytes as UTF-8 and rewrites line endings,
    so a retry comparing an existing asset would either raise a decode error or
    — worse — compare data that the transport had already altered and declare a
    conflict that does not exist. The bytes have to arrive unmodified, which
    also matters for the catalog: its sidecar checksum covers exact bytes.
    """
    result = subprocess.run(["gh", *args], capture_output=True)
    if result.returncode != 0:
        raise HistoryError(f"gh {' '.join(args[:3])} failed: "
                           f"{result.stderr.decode('utf-8', 'replace').strip()}")
    return result.stdout


def list_releases(repo):
    """Every release — drafts included — with its asset names and ids.

    Uses the paginated API rather than `gh release list --limit N`: a limit is
    a silent truncation, and the releases it would drop are the oldest ones,
    which are exactly where a reused release number would be hiding.
    """
    raw = gh("api", "--paginate", f"repos/{repo}/releases",
             "--jq", '.[] | {tag: .tag_name, draft: .draft, '
                     'assets: [.assets[] | {name: .name, id: .id}]}')
    releases = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            releases.append(json.loads(line))
        except ValueError as error:
            raise HistoryError(f"could not parse release listing: {error}")
    return releases


def fetch_asset(repo, asset_id):
    return gh_bytes("api", "-H", "Accept: application/octet-stream",
                    f"repos/{repo}/releases/assets/{asset_id}")


def raw_asset_of(repo, release, name, fetch=None):
    """One named asset's RAW BYTES, or None only when it positively has none."""
    assets = release.get("assets")
    if assets is None:
        raise HistoryError(f"release {release.get('tag')!r} returned no asset list")
    match = next((asset for asset in assets if asset.get("name") == name), None)
    if match is None:
        # Confirmed absent: the asset list loaded and does not contain one.
        # This is the ONLY path that contributes nothing without failing.
        return None
    body = (fetch or fetch_asset)(repo, match["id"])
    # Test fetchers may hand back text; the real one always returns bytes.
    return body.encode() if isinstance(body, str) else body


def upload(repo, tag, paths, releases=None, fetch=None):
    """Attach assets, comparing rather than overwriting anything already there.

    A retry must be able to finish a partially-uploaded release, but --clobber
    is the wrong tool for it: replacing a published artifact silently changes
    what an existing release means, and anyone who already downloaded it has
    something the release no longer claims. So an identical asset is skipped
    and a differing one is an error.
    """
    listing = list_releases(repo) if releases is None else releases
    current = next((release for release in listing if release.get("tag") == tag), None)
    results = []
    for path in paths:
        path = Path(path)
        name = path.name
        existing = None if current is None else raw_asset_of(repo, current, name, fetch=fetch)
        if existing is not None:
            if existing == path.read_bytes():
                results.append(f"{name}: already published and identical, skipped")
                continue
            raise HistoryError(
                f"{tag} already has a DIFFERENT {name} attached. A published asset is never "
                f"overwritten; if these bytes are correct they belong under a new release.")
        gh("release", "upload", tag, str(path), "--repo", repo)
        results.append(f"{name}: uploaded")
    return results


def raw_catalog_of(repo, release, fetch=None):
    """The release's catalog as RAW BYTES, or None only when it positively has none."""
    return raw_asset_of(repo, release, CATALOG_ASSET, fetch=fetch)


def catalog_of(repo, release, fetch=None):
    """The release's agent catalog, parsed, or None when it positively has none."""
    raw = raw_catalog_of(repo, release, fetch=fetch)
    if raw is None:
        return None
    try:
        return json.loads(raw)
    except ValueError as error:
        raise HistoryError(f"release {release.get('tag')!r} has an unreadable "
                           f"{CATALOG_ASSET}: {error}")


def gather(repo, releases=None, fetch=None):
    """Build {release_number: [artifacts, ...]}, retaining EVERY catalog.

    Never collapsed to one entry per number: if a number was published twice
    with different bytes, collapsing would overwrite the first with the second
    and destroy the very evidence the identity gate exists to find.
    """
    history = {}
    for release in (list_releases(repo) if releases is None else releases):
        catalog = catalog_of(repo, release, fetch=fetch)
        if catalog is None:
            continue
        number = catalog.get("agent_release")
        artifacts = catalog.get("artifacts")
        if not isinstance(number, int) or isinstance(number, bool) or not isinstance(artifacts, dict):
            raise HistoryError(f"release {release.get('tag')!r} has a malformed {CATALOG_ASSET}")
        history.setdefault(str(number), []).append(artifacts)
    return history


def persist(repo, tag, catalog_path, releases=None, fetch=None):
    """Attach the catalog to this tag's draft release, idempotently.

    Equality is RAW BYTES, not parsed JSON. agent-manifest.sha256 is a checksum
    over the catalog's exact bytes, so two semantically identical documents that
    serialise differently are not interchangeable here: keeping the old
    formatting while publishing a sidecar computed over the new formatting
    produces a checksum that does not match the file it names, and every
    download that verifies it fails.
    """
    candidate = Path(catalog_path).read_bytes()
    listing = list_releases(repo) if releases is None else releases
    current = next((release for release in listing if release.get("tag") == tag), None)

    if current is not None:
        existing = raw_catalog_of(repo, current, fetch=fetch)
        if existing is not None:
            if existing == candidate:
                return f"{CATALOG_ASSET} already published for {tag} and matches; nothing to do"
            raise HistoryError(
                f"{tag} already has a DIFFERENT {CATALOG_ASSET} attached (byte comparison).\n"
                f"A published catalog is evidence of what that release carries and is never "
                f"overwritten; its sidecar checksum covers those exact bytes. If these bytes "
                f"are correct, they belong under a new release.")

    gh("release", "upload", tag, str(catalog_path), "--repo", repo)
    return f"{CATALOG_ASSET} persisted for {tag} before promotion"


def build_record(version, revision, hub, dashboard):
    """The intent document, validated on the way in as well as on the way out.

    Refs must be canonical and digest-pinned. A tag is a moving target, and the
    entire point of recording the pair is that a retry lands on exactly the
    bytes the first attempt verified.

    Every field is type-checked before it is matched. This parses a file that
    may have been hand-edited or truncated, so a non-string where a string
    belongs has to become a HistoryError the caller already handles, not a
    TypeError out of `re`.
    """
    for label, value in (("version", version), ("revision", revision),
                         ("hub image", hub), ("dashboard image", dashboard)):
        if not isinstance(value, str):
            raise HistoryError(f"{label} must be a string, got {type(value).__name__}: {value!r}")
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise HistoryError(f"not a release tag: {version!r}")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise HistoryError(f"not a full revision: {revision!r}")
    for name, ref in (("hub", hub), ("dashboard", dashboard)):
        if not IMAGE_REF.fullmatch(ref) or not ref.startswith(f"ghcr.io/bokiko/bloxos-{name}@"):
            raise HistoryError(f"{name} image must be a canonical digest-pinned BloxOS ref: {ref!r}")
    return {"schema": BUILD_SCHEMA, "version": version, "revision": revision,
            "images": {"hub": hub, "dashboard": dashboard}}


def parse_build_record(raw, tag):
    try:
        record = json.loads(raw)
    except ValueError as error:
        raise HistoryError(f"release {tag!r} has an unreadable {BUILD_ASSET}: {error}")
    if not isinstance(record, dict):
        raise HistoryError(f"release {tag!r} has a malformed {BUILD_ASSET}")
    schema = record.get("schema")
    # `True == 1` in Python, so a bare equality check accepts "schema": true.
    if not isinstance(schema, int) or isinstance(schema, bool) or schema != BUILD_SCHEMA:
        raise HistoryError(f"release {tag!r} has {BUILD_ASSET} schema {schema!r}, "
                           f"expected {BUILD_SCHEMA}. Refusing to guess what it means.")
    images = record.get("images")
    if not isinstance(images, dict):
        raise HistoryError(f"release {tag!r} has {BUILD_ASSET} with no image pair")
    # Revalidate through the same constructor, so a hand-edited or truncated
    # asset cannot supply a tag-shaped ref that a later step would resolve.
    return build_record(record.get("version"), record.get("revision"),
                        images.get("hub"), images.get("dashboard"))


def lookup_build(repo, tag, version, revision, releases=None, fetch=None):
    """The verified image pair for this tag, or None only when positively absent.

    Absence is the ONLY outcome that means "build". A listing failure, a
    download failure, an unreadable asset or a record describing a different
    source all raise: treating any of them as absence would rebuild the images
    and promote tags that disagree with a catalog already published under this
    release, which is precisely the state that cannot be recovered from.
    """
    listing = list_releases(repo) if releases is None else releases
    current = next((release for release in listing if release.get("tag") == tag), None)
    if current is None:
        return None  # no release for this tag at all: nothing has been built
    raw = raw_asset_of(repo, current, BUILD_ASSET, fetch=fetch)
    if raw is None:
        return None  # confirmed absent
    record = parse_build_record(raw, tag)
    mismatched = {key: (record[key], want)
                  for key, want in (("version", version), ("revision", revision))
                  if want is not None and record[key] != want}
    if mismatched:
        raise HistoryError(
            f"{tag} already has a {BUILD_ASSET} describing a DIFFERENT build: "
            + "; ".join(f"{key} is {got!r}, this run is {want!r}"
                        for key, (got, want) in sorted(mismatched.items()))
            + ". A release number means one set of bytes; these belong under a new release.")
    return record


def ensure_draft(repo, tag, prerelease, notes, releases=None, create=None):
    """Make sure a DRAFT release exists for this tag, without guessing.

    Both callers previously did `gh release view ... || gh release create ...`,
    which reads ANY failure as "no release exists". An expired token, a rate
    limit or a network blip would then try to create a release that is already
    published — and on a retry, the branch that was supposed to assert the
    release is still a draft would instead silently take the create path.

    The listing either loads or raises, and only a listing that positively does
    not contain this tag is treated as absence.
    """
    listing = list_releases(repo) if releases is None else releases
    current = next((release for release in listing if release.get("tag") == tag), None)
    if current is not None:
        if not current.get("draft"):
            raise HistoryError(
                f"{tag} is already PUBLISHED. Release assets are attached to drafts and a "
                f"published release is never modified by the pipeline.")
        return f"{tag} draft already exists"
    flags = ["--prerelease"] if prerelease else []
    (create or gh)("release", "create", tag, "--repo", repo, "--verify-tag", "--draft",
                   *flags, "--title", f"BloxOS {tag}", "--notes", notes)
    return f"{tag} draft created"


def persist_build(repo, tag, build_path, releases=None, fetch=None):
    """Attach the verified image pair, idempotently and never destructively.

    Called AFTER identity validation and BEFORE the agent catalog is persisted
    or any tag is promoted. A run that dies after this leaves a record of a pair
    that was verified but not promoted — which is exactly what the retry needs,
    and is harmless if the release is abandoned. Dying BEFORE it leaves nothing,
    and the retry simply builds again.
    """
    candidate = Path(build_path).read_bytes()
    parse_build_record(candidate, tag)  # never publish something we could not read back
    listing = list_releases(repo) if releases is None else releases
    current = next((release for release in listing if release.get("tag") == tag), None)
    if current is not None:
        existing = raw_asset_of(repo, current, BUILD_ASSET, fetch=fetch)
        if existing is not None:
            if existing == candidate:
                return f"{BUILD_ASSET} already published for {tag} and matches; nothing to do"
            raise HistoryError(
                f"{tag} already has a DIFFERENT {BUILD_ASSET} attached (byte comparison). "
                f"The image pair a release was built from is never rewritten.")
    gh("release", "upload", tag, str(build_path), "--repo", repo)
    return f"{BUILD_ASSET} persisted for {tag} before the catalog and before promotion"


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)

    g = commands.add_parser("gather", help="emit release -> [artifacts] as JSON")
    g.add_argument("--repo", required=True)
    g.add_argument("--output", required=True, type=Path)

    p = commands.add_parser("persist", help="attach a verified catalog to the draft release")
    p.add_argument("--repo", required=True)
    p.add_argument("--tag", required=True)
    p.add_argument("--catalog", required=True, type=Path)

    u = commands.add_parser("upload", help="attach assets without ever overwriting one")
    u.add_argument("--repo", required=True)
    u.add_argument("--tag", required=True)
    u.add_argument("files", nargs="+", type=Path)

    l = commands.add_parser("lookup-build",
                            help="resolve the verified image pair for a retry, or report absence")
    l.add_argument("--repo", required=True)
    l.add_argument("--tag", required=True)
    l.add_argument("--version", required=True)
    l.add_argument("--revision", required=True)
    l.add_argument("--output", type=Path,
                   help="where to write the recovered record, when one exists")

    pb = commands.add_parser("persist-build", help="attach the verified image pair immutably")
    pb.add_argument("--repo", required=True)
    pb.add_argument("--tag", required=True)
    pb.add_argument("--build", required=True, type=Path)

    d = commands.add_parser("ensure-draft", help="create the draft release only if it is truly absent")
    d.add_argument("--repo", required=True)
    d.add_argument("--tag", required=True)
    d.add_argument("--notes", required=True)

    rb = commands.add_parser("record-build", help="write the image-pair intent document")
    rb.add_argument("--version", required=True)
    rb.add_argument("--revision", required=True)
    rb.add_argument("--hub", required=True)
    rb.add_argument("--dashboard", required=True)
    rb.add_argument("--output", required=True, type=Path)

    args = parser.parse_args()
    try:
        if args.command == "gather":
            history = gather(args.repo)
            args.output.write_text(json.dumps(history, indent=2, sort_keys=True) + "\n")
            print(f"gathered {len(history)} agent release(s) from published and draft releases")
        elif args.command == "persist":
            print(persist(args.repo, args.tag, args.catalog))
        elif args.command == "ensure-draft":
            print(ensure_draft(args.repo, args.tag, "-" in args.tag, args.notes))
        elif args.command == "record-build":
            record = build_record(args.version, args.revision, args.hub, args.dashboard)
            args.output.write_text(json.dumps(record, indent=2, sort_keys=True) + "\n")
            print(f"recorded the image pair for {args.version}")
        elif args.command == "persist-build":
            print(persist_build(args.repo, args.tag, args.build))
        elif args.command == "lookup-build":
            record = lookup_build(args.repo, args.tag, args.version, args.revision)
            # key=value on stdout, for >> "$GITHUB_OUTPUT". Absence is reported
            # as a value, never as a non-zero exit: only a FAILED lookup exits
            # non-zero, so the workflow cannot confuse the two.
            if record is None:
                print("reuse=false")
            else:
                if args.output is not None:
                    args.output.write_text(json.dumps(record, indent=2, sort_keys=True) + "\n")
                print("reuse=true")
                print("hub=" + record["images"]["hub"])
                print("dashboard=" + record["images"]["dashboard"])
        else:
            for line in upload(args.repo, args.tag, args.files):
                print(line)
    except HistoryError as error:
        sys.stderr.write(f"release history: {error}\n")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
