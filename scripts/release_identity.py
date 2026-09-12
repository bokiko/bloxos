#!/usr/bin/env python3
"""Release identity gate: a (platform, release) pair names exactly one set of bytes.

The agent release number is how the fleet decides whether an offer is newer
than what it runs. If the same number can ever name different bytes, two
machines both "on release 9" are running different binaries, and the number
stops describing anything.

What that actually breaks is the rollout, not the rollback floor. A protocol-2
agent pins release number AND sha, so it REJECTS an offer carrying its own
number with different bytes — correctly, and permanently: the fleet simply
stops taking the update, and every affected machine stalls on a build nobody
can replace without a number bump. Older protocol-1 agents do not enforce that
pairing at all, so for them the same ambiguity is silent.

This runs BEFORE promotion, because after a tag is published the ambiguity is
permanent.

Four things it enforces:

  * A release number already used with DIFFERENT bytes can never be reused.
  * A retry of the same release with IDENTICAL bytes is idempotent and passes,
    so a re-run of a failed workflow is not a dead end.
  * A new release number must exceed the historical floor, so numbering only
    ever moves forward.
  * Both hub image platforms must embed the SAME agent bytes as the canonical
    standalone bundle.

That last one is not paranoia. Native server exports copy the canonical bundle,
but a containerised arm64 hub serves the agents built into its own image. The
two images are built separately, on different platforms, from build args that
could drift. Sharing a hub image index says nothing about what those images
embedded — so the payload maps are compared directly.

History is passed IN. Remote lookup belongs in the workflow, where credentials
and the network live; this file stays a pure decision so it can be tested.
"""
import argparse
import json
from pathlib import Path
import sys

# Every agent platform a catalog must describe. A catalog short of one would
# let that platform's identity go unchecked while the others reported fine.
AGENT_PLATFORMS = frozenset({"linux/amd64", "linux/arm64", "windows/amd64"})

# Both hub image platforms must be presented. A non-empty check is not enough:
# supplying only amd64 would "pass" while arm64 — the one built on a different
# platform, and therefore the one that can actually drift — went unexamined.
HUB_IMAGE_PLATFORMS = frozenset({"linux/amd64", "linux/arm64"})

HEX = "0123456789abcdef"


class IdentityError(Exception):
    """A release that must not be promoted."""


def artifact_map(catalog):
    """The identity of a catalog: platform -> (sha256, size). Nothing else.

    File names and provenance are deliberately excluded. Renaming a payload
    does not make it different bytes, and provenance legitimately differs
    between an in-image catalog and a release catalog describing the same
    binaries.
    """
    artifacts = catalog.get("artifacts")
    if not isinstance(artifacts, dict):
        raise IdentityError("catalog declares no artifacts")
    if set(artifacts) != AGENT_PLATFORMS:
        raise IdentityError(
            f"catalog must describe exactly {sorted(AGENT_PLATFORMS)}, got {sorted(artifacts)}")
    identity = {}
    for platform, artifact in artifacts.items():
        if not isinstance(artifact, dict):
            raise IdentityError(f"catalog entry for {platform} is not an object")
        sha = str(artifact.get("sha256", "")).lower()
        size = artifact.get("size")
        # bool is an int in Python, and True would sail through a > 0 test.
        if len(sha) != 64 or any(character not in HEX for character in sha):
            raise IdentityError(f"catalog entry for {platform} has no usable sha256")
        if not isinstance(size, int) or isinstance(size, bool) or size <= 0:
            raise IdentityError(f"catalog entry for {platform} has no usable size")
        identity[platform] = (sha, size)
    return identity


def release_of(catalog):
    release = catalog.get("agent_release")
    if not isinstance(release, int) or isinstance(release, bool) or release <= 0:
        raise IdentityError("catalog declares no usable agent_release")
    return release


def describe(identity):
    return ", ".join(f"{platform}={sha[:12]}…" for platform, (sha, _) in sorted(identity.items()))


def check_history(candidate, history):
    """Compare the candidate against every catalog already published or drafted.

    `history` maps release number -> LIST of artifact maps, one per catalog
    found. Every catalog is retained rather than collapsed to one map per
    number, because collapsing is itself a way to lose the finding: if a number
    was published twice with different bytes, the second would simply overwrite
    the first and the ambiguity would be invisible to the check meant to catch
    it. Divergence is therefore detected generically, from the data, with no
    release number special-cased — including release 7, whose existing
    divergence this gate discovers rather than assumes.

    History MUST include draft releases: the workflow promotes Docker tags
    before it attaches draft assets, so a release whose images are already live
    can still be a draft. Gating on published releases alone would read such a
    release as absent and wave through a conflicting reuse of its number.
    """
    if not history:
        # This repository has published agent releases. An empty history means
        # the gatherer failed, and "we found nothing" must never be allowed to
        # establish a floor of zero and approve any number at all.
        raise IdentityError(
            "no published agent release history was found; this repository has "
            "published releases, so an empty history means the lookup failed")

    release = release_of(candidate)
    identity = artifact_map(candidate)
    floor = max(history)
    known = history.get(release)

    if known is not None:
        distinct = {tuple(sorted(entry.items())) for entry in known}
        if len(distinct) > 1:
            raise IdentityError(
                f"release {release} has already been published with {len(distinct)} different "
                f"sets of bytes, so what that number identifies is ambiguous and it can never "
                f"be reused; bump the agent release instead")
        if known[0] != identity:
            raise IdentityError(
                f"release {release} is already published with DIFFERENT bytes\n"
                f"  published: {describe(known[0])}\n"
                f"  candidate: {describe(identity)}\n"
                f"a release number names one set of bytes; bump the agent release")
        # Identical bytes, so nothing about that release changes. But promoting
        # it now would still offer the fleet an OLDER identity than the one
        # already published, which is a downgrade dressed as a retry.
        if release < floor:
            raise IdentityError(
                f"release {release} matches its published bytes, but release {floor} has since "
                f"been published; re-running an older release must not lower the offered "
                f"release identity")
        return f"release {release} already published with identical bytes; idempotent retry"

    if release <= floor:
        raise IdentityError(
            f"release {release} does not exceed the published floor {floor}; "
            f"numbering must move forward")
    return f"release {release} is new and above the floor {floor}"


def check_image_platforms(candidate, image_catalogs):
    """Every hub image must embed the same agent bytes as the canonical bundle."""
    missing = HUB_IMAGE_PLATFORMS - set(image_catalogs)
    if missing:
        raise IdentityError(
            f"no catalog supplied for hub image platform(s) {sorted(missing)}; "
            f"an unexamined platform must not read as a passing one")
    unexpected = set(image_catalogs) - HUB_IMAGE_PLATFORMS
    if unexpected:
        raise IdentityError(f"unexpected hub image platform(s) {sorted(unexpected)}")
    canonical = artifact_map(candidate)
    canonical_release = release_of(candidate)
    for platform, catalog in sorted(image_catalogs.items()):
        if release_of(catalog) != canonical_release:
            raise IdentityError(
                f"hub image {platform} embeds agent release {release_of(catalog)}, "
                f"canonical bundle says {canonical_release}")
        embedded = artifact_map(catalog)
        if embedded != canonical:
            raise IdentityError(
                f"hub image {platform} embeds different agent bytes than the canonical bundle\n"
                f"  image:     {describe(embedded)}\n"
                f"  canonical: {describe(canonical)}\n"
                f"a build-arg or toolchain drift between image platforms would do this; "
                f"sharing an image index does not make the embedded payloads equal")
    return f"all {len(image_catalogs)} hub image platforms embed the canonical agent bytes"


def load_history(raw):
    """Parse {"<release>": [artifacts, ...]} into release -> list of artifact maps.

    A LIST per release, never a single map: see check_history. A lone object is
    accepted and wrapped, so a gatherer that emits one catalog per number is
    not silently misread as something else.
    """
    history = {}
    for release, entries in (raw or {}).items():
        if isinstance(entries, dict):
            entries = [entries]
        if not isinstance(entries, list) or not entries:
            raise IdentityError(f"history for release {release} is not a list of catalogs")
        history[int(release)] = [artifact_map({"artifacts": entry}) for entry in entries]
    return history


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--candidate", required=True, type=Path,
                        help="the canonical standalone agent-manifest.json")
    parser.add_argument("--history", required=True, type=Path,
                        help="JSON of release -> artifacts, gathered from published AND draft releases")
    parser.add_argument("--image-catalog", action="append", default=[], metavar="PLATFORM=PATH",
                        help="agent catalog extracted from one hub image; repeat per platform")
    args = parser.parse_args()

    candidate = json.loads(args.candidate.read_text())
    history = load_history(json.loads(args.history.read_text()))
    catalogs = {}
    for entry in args.image_catalog:
        platform, _, path = entry.partition("=")
        if not platform or not path:
            parser.error(f"--image-catalog expects PLATFORM=PATH, got {entry!r}")
        catalogs[platform] = json.loads(Path(path).read_text())

    try:
        print(check_history(candidate, history))
        print(check_image_platforms(candidate, catalogs))
    except IdentityError as error:
        sys.stderr.write(f"release identity: {error}\n")
        return 1
    print("Release identity verified. Safe to promote.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
