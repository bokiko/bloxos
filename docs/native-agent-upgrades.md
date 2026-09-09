# Native hub: check and stage agent updates

Updating the hub executable does **not** update separately installed agent
files. A machine that matches the hub's offered SHA is up to date **with that
offered file**, not necessarily with the latest BloxOS release.

### Agent is running, but drops offline after enrollment

Very old agents save the issued secret without sending the enrollment
confirmation required by newer hubs. The symptom is a successful first
connection followed by a disconnect about 30 seconds later and repeated
WebSocket handshake failures. `systemctl` can still report `active (running)`:
that describes the process, not its connection to BloxOS. Hub logs are needed
to distinguish this from proxy, network or other authentication failures.

Use the current official agent payloads, not a rebuild of an old source tree.
The onboarding download rejects unnumbered payloads because their enrollment
compatibility cannot be established. Normal signed-update downloads remain
available for existing agents; this check is specific to new installations.

For a stranded machine, stage and activate compatible payloads using the steps
below, then generate a fresh **Add Machine** command and run it again. A secret
issued by the old incomplete handshake was never committed by the hub; merely
restarting that old agent does not repair it. Do not delete the machine's CA,
update key or rollback floor. If the hub already has an active credential for
that machine, use explicit credential recovery instead of attempting to replace
it with a generic install token.

Docker hub images include the agent payloads. Native installations can use the
three payloads attached to newer GitHub releases, extracted from those exact
images without rebuilding:

| Platform | Release asset |
| --- | --- |
| Linux x86-64 | `bloxos-agent-linux-amd64` |
| Linux ARM64 | `bloxos-agent-linux-arm64` |
| Windows x86-64 | `bloxos-agent-windows-amd64.exe` |

## 1. Download and check — no rollout

Get the three binaries, `agent-manifest.json` and `agent-manifest.sha256` from
the same approved [GitHub release](https://github.com/bokiko/bloxos/releases).
Use `scripts/agent_bundle.py` from that release's source checkout with Python
3.9 or newer on Linux or macOS. Earlier releases may not have these assets.

The manifest records the source commit, published image digest, agent release
number, architecture, size and SHA-256 of every payload. Obtain its SHA-256
through the trusted release handoff; downloading a checksum alongside a file
detects corruption but does not independently authenticate the publisher.

```sh
python3 scripts/agent_bundle.py check \
  --bundle /path/to/downloaded-assets \
  --manifest-sha256 <trusted-64-character-manifest-sha256>
```

This reads files only. It checks all three targets, including ELF/PE
architecture and matching, nonzero embedded release numbers. It rejects
missing, modified or unnumbered binaries. It does not contact the hub, run a
binary, sign anything, install anything or change the fleet.

## 2. Stage — still no rollout

Choose a private, dedicated staging directory **outside every active agent
serve directory**. Do not run this helper as root. Do not place staging under
a directory that the hub watches or configure the hub to serve staging files.

```sh
mkdir -m 700 /path/to/agent-staging
python3 scripts/agent_bundle.py stage \
  --bundle /path/to/downloaded-assets \
  --manifest-sha256 <trusted-64-character-manifest-sha256> \
  --staging-root /path/to/agent-staging
```

The helper creates a new `agent-release-N` directory with read-only files and
checks the copied bytes again. It refuses to overwrite an existing release.
Use `check` on that existing directory if already staged. Failed validation
does not change active binaries.

If the computer loses power during staging, an incomplete destination may
remain. `check` will reject it. Keep it for inspection and use a new dedicated
staging root; do not activate a partially staged release. Staging is not a
backup or a power-loss durability guarantee.

Known native serve directories and the binary-path overrides visible in the
calling shell are rejected as staging roots. The helper cannot discover a
service's hidden environment: **you must check the hub service configuration**
for `BLOXOS_AGENT_BINARY`, `BLOXOS_AGENT_BINARY_ARM64` and
`BLOXOS_AGENT_BINARY_WINDOWS` before choosing a staging location.

## 3. Signing and activation are separate

Checksums are not fleet update signatures. An offline-signing installation
must have its existing offline key holder sign each exact payload for the
appropriate OS, and verify the signatures against the fleet's pinned public
key. Never upload the private key to the hub. See
[offline update signing](offline-update-signing.md).

Installing a staged payload into an **active** serve path, or changing the
hub's configured serve path to it, is activation. It can announce a fleet-wide
update — even when the official payload is still numbered release 7 and the
old served file has no release marker. Keep signing and activation behind
your explicit rollout procedure; staging alone is not approval to activate.

Protocol-2 agents reject older releases and equal-number/different-SHA
payloads. Reuse the exact published bytes. If you rebuild and the bytes change,
the source-controlled agent release number must advance before publication.
Do not rebuild release 7 differently and label it release 7. Old protocol-1
agents do not enforce this rollback floor; a matching offered SHA does not
prove that protection is present.

## Release maintainers

`scripts/export-agent-bundle.sh IMAGE@sha256:DIGEST SOURCE_SHA vX.Y.Z NEW_DIR`
extracts files from a never-started, network-disabled Docker container,
checks the image's source revision and generates the manifest. It does not
compile new binaries. The tagged release workflow uploads these assets to a
draft GitHub release after the image checks pass. Review that draft and its
checksums before publishing; fleet-specific signatures are not included.

Tags use `vX.Y.Z` or a Docker-compatible prerelease such as `vX.Y.Z-rc.1`.
The shared tag validator runs before any images are published. Prereleases
get versioned image tags and a prerelease draft, but do not replace `latest`.
Build-metadata suffixes (`+...`) and arbitrary `v...` names are rejected before
publication because they cannot be used as these release image tags.

Uploads deliberately do not overwrite existing assets or modify a published
release. A rerun encountering either condition stops for maintainer review.
For a partial draft upload, compare the existing asset hashes first; do not
blindly clobber previously published agent bytes. A failed export may leave
its new output directory for inspection, but its temporary Docker container
is removed on a normal/error exit.
