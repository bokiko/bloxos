# Agent delivery: how the fleet gets new agents

**A packaged hub carries the agent binaries it serves**, and upgrading the hub
upgrades what those platforms are offered. No payload staging step is required.

Two things still are, where they apply:

- **An explicit per-platform override keeps winning.** A platform pinned with
  `BLOXOS_AGENT_BINARY_ARM64` or the like stays operator-managed; the bundle
  does not override it. Check each platform's `source` on the Versions page.
- **An offline-signing install must still authorise the new bytes.** Delivery
  moves binaries; it does not sign them. See [Signatures](#signatures).

Previously `bloxos-update` did not touch agent files, so a hub could run a new
release while serving agents built weeks earlier. Machines reported "matches
offered build" correctly — the offer was stale.

## Where agents live now

The hub looks for a managed bundle in `agents/` **beside its own executable**:

| Install | Hub executable | Bundle |
| --- | --- | --- |
| Native | `<release>/hub/bloxos-hub` | `<release>/hub/agents/` |
| Docker | `/usr/local/bin/bloxos-hub` | `/usr/local/bin/agents/` |

Both server archives carry all three agent platforms, so a hub on either
server architecture can serve any machine in the fleet.

In the Docker image the older paths
(`/usr/local/lib/bloxos/linux/<arch>/bloxos-agent` and
`/usr/local/lib/bloxos/windows/bloxos-agent.exe`) are **hard links** to the
managed payloads. An existing `BLOXOS_AGENT_BINARY` that points at one keeps
working, and because they share an inode they cannot drift from the bundle.

### A bad bundle stops the hub

A packaged build that cannot produce a valid bundle **refuses to start**,
before it opens the database. Missing, corrupt, mislabelled or
wrong-architecture payloads are all fatal. This is deliberate: falling back to
whatever binaries happen to be on disk, while reporting healthy, is exactly the
staleness this replaces. Because nothing has been written yet, the updater's
readiness check rejects the candidate and rolls back.

The hub validates each payload's SHA-256 and size against the catalog, reads
the architecture out of the ELF or PE header rather than trusting the
catalog's label, and checks the release marker compiled into each binary
against the release the catalog claims.

## Precedence

Each platform has one override variable: `BLOXOS_AGENT_BINARY` for Linux
amd64, `BLOXOS_AGENT_BINARY_ARM64` for Linux arm64, and
`BLOXOS_AGENT_BINARY_WINDOWS` for Windows. **A platform's own override
outranks the managed bundle** and is authoritative — that platform resolves it
or fails, never falling back.

**Linux amd64**

1. `BLOXOS_AGENT_BINARY` — authoritative, fails closed. The one exception is
   below.
2. the managed bundle
3. `/usr/local/lib/bloxos/linux/amd64/bloxos-agent`
4. `/usr/local/lib/bloxos/linux/bloxos-agent` (legacy)
5. a `bloxos-agent` sibling of the hub executable

**Linux arm64**

1. `BLOXOS_AGENT_BINARY_ARM64` — authoritative, fails closed, and the sole
   candidate when set
2. a generic `BLOXOS_AGENT_BINARY` other than the shipped default path —
   non-authoritative and architecture-verified, so a binary built for another
   architecture is skipped rather than failing the platform
3. the managed bundle
4. `/usr/local/lib/bloxos/linux/arm64/bloxos-agent`
5. `/usr/local/lib/bloxos/linux/bloxos-agent` (legacy)
6. a `bloxos-agent` sibling of the hub executable

**Windows**

1. `BLOXOS_AGENT_BINARY_WINDOWS` — authoritative, fails closed
2. the managed bundle
3. `/usr/local/lib/bloxos/windows/bloxos-agent.exe`
4. a `bloxos-agent.exe` sibling of the hub executable

### The one exception

A `BLOXOS_AGENT_BINARY` whose value is **exactly** the shipped default path
(`/usr/local/lib/bloxos/linux/bloxos-agent`) is treated as legacy
configuration, and the managed bundle takes precedence over it. The project's
own systemd unit sets that variable, so without this exception the bundle would
never be reached on a standard native install. Any other value — including a
path that merely normalises to the default — remains an authoritative operator
pin.

Set `BLOXOS_AGENT_DELIVERY=external` to restore the full legacy resolver and
manage agent binaries yourself. The manual procedure below then applies.

## Signatures

An offline install (one with `BLOXOS_UPDATE_PUBKEY` and no private key on the
hub) can only announce a signature produced in advance. Because the hub's
agents now live in a release directory that changes with every upgrade, there
is nowhere stable to put a `<binary>.sig` for bytes you have not received yet.

Signatures are therefore also looked up by **content address** in
`~/.bloxos/agent-signatures/` (override with `BLOXOS_AGENT_SIGNATURE_DIR`):

```
<os>-<arch>-<sha256>.sig
```

base64 ed25519, over the same `bloxos-agent-update:v1:<os>:<sha256>` message as
before. You can authorise bytes **before** the hub serves them; a signature for
a release that has not arrived simply waits unused.

Old signatures are never copied or regenerated for new bytes. A detached
signature covers one specific SHA, so for different bytes it is not weak — it
cannot verify at all, and announcing it would make every agent reject the
update.

Lookup order is `<binary>.sig`, then the content-addressed store, then the
hub-held key. A hub that holds its own signing key needs **no pre-staged
signature, no new key and no manual step**: the store is consulted, finds
nothing, and the hub signs the bytes itself. When nothing can authorise a
build, the hub logs the exact path that would fix it.

The store holds data the hub verifies cryptographically, not an executable it
serves, so it follows the installation-owned rule: owned by the user the hub
runs as (or root) and not group- or other-writable. Under Docker that is uid
65532 with `/data`; requiring root there would break every containerised
install for no benefit, since an unverifiable signature is discarded anyway.

---

# Manual staging (external delivery only)

Everything below applies when you have set `BLOXOS_AGENT_DELIVERY=external`,
are running a hub built before managed delivery, or are managing one platform
with an explicit override. On a packaged hub in the default `auto` mode, the
payload staging in this section is not needed — offline signature
authorisation, above, still is.

A machine that matches the hub's offered SHA is up to date **with that offered
file**, not necessarily with the latest BloxOS release.

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
update, including when the old served file carries no release marker at all.
Keep signing and activation behind your explicit rollout procedure; staging
alone is not approval to activate.

Protocol-2 agents reject older releases and equal-number/different-SHA
payloads. Reuse the exact published bytes. If you rebuild and the bytes change,
the source-controlled agent release number must advance before publication.
Do not rebuild release 7 differently and label it release 7. Old protocol-1
agents do not enforce this rollback floor; a matching offered SHA does not
prove that protection is present.

This is now enforced in CI rather than left to care. Before any tag is
promoted, a gate compares the candidate against every agent catalog this
repository has published, drafts included, and refuses to proceed if the
release number is already in use for different bytes or does not exceed the
published floor. It also checks that both hub image platforms embed the same
agent bytes as the standalone bundle, since the two images are built
separately and could otherwise drift.

Release 7 is a live example: ten published catalogs claim it, carrying two
distinct sets of bytes. The gate finds this from the published history rather
than from a hardcoded rule, and that number can never be reused.

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
release. A rerun compares an already-published asset byte for byte and skips it
when identical, and stops for maintainer review when it differs; nothing uses
`--clobber`. The verified catalog is attached to the draft release **before**
the Docker tags are promoted, so a run that dies mid-release still leaves a
durable record of what agent bytes those images carry. A failed export may leave
its new output directory for inspection, but its temporary Docker container
is removed on a normal/error exit.
