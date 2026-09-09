# Offline agent release signing

This runbook prepares and signs Linux and Windows agent releases on an offline signing host,
then installs the binary/signature pairs into root-owned serve directories on
the hub. The private Ed25519 key stays on the offline build host.

> **Scope boundary:** this document describes the later one-time keyless
> cutover, but reading or staging this runbook does not authorize it. Do not set
> `BLOXOS_UPDATE_PUBKEY`, move or remove the hub's private key, restart the hub,
> or deploy an agent release without a separate, environment-specific approval.

## Invariants

- Prefer the exact published native payloads; record their approved manifest
  and hashes. A native hub rebuild does not replace separate served agent files.
  If making a new agent build, use a pinned, clean commit and record it too.
- For each new agent release, bump both the release number and matching marker
  in `agent/release.go` before building. A rebuild that changes the binary SHA
  also needs a new number; equal-number/different-SHA updates are refused by
  protocol-2 agents. Use the same number across the platform builds of a release.
- The signing input and private key remain on the offline signing host. Never copy the private
  key to the hub or print it in a terminal transcript.
- `bloxos-sign` and both agents use `updatesigning.Message`; the signed bytes
  are `bloxos-agent-update:v1:<os>:<sha256>` after trimming and lowercasing the
  OS and SHA.
- Protocol-2 agents extract the release number from the signed binary itself.
  No extra sidecar, signing command, or onboarding step is required. Their local
  release/SHA floor survives key rotation and installer re-runs. See
  [update recovery](agent-update-recovery.md) for migration and recovery limits.
- A detached signature is standard-base64 Ed25519 in `<binary>.sig`, with one
  trailing newline. The hub verifies it against the configured public key
  before announcing it.
- The hub's configured Linux and Windows binary paths must be explicit and
  rooted in a `root:root` chain that is not group- or other-writable. Do not
  rely on relative paths or fallbacks.
- Pause rollout before changing a served artifact and confirm success.
  On hubs with the durable operator-pause fix, a manual pause is saved in
  `hub_settings` and survives both served-SHA changes and hub restarts. Only
  an explicit Resume clears it. A database error must not be mistaken for a
  successful pause/resume. The automatic failure breaker is separate and may
  reset when a served binary changes.
- **v1.2.0 and earlier do not have durable pause.** Upgrade the hub first, or
  keep the hub stopped and agent connectivity controlled during activation.
  Never rely on racing to re-pause after replacing an artifact.
  Downgrading to an older hub also loses enforcement of the saved pause;
  control agent connectivity before such a hub rollback.
- Pause prevents new announcements, not an update already announced or in
  flight. Before activation, inspect pending updates and confirm they have
  settled. Resume is fleet-wide; this is not a per-machine canary mechanism.

## 1. Prepare exact payloads on the offline signing host

For an existing official agent release, transfer the approved published
payloads and manifest to the offline host and verify their hashes there.
Do not rebuild the same release number with different bytes. Build only the
`bloxos-sign` utility from the approved source when signing existing payloads;
skip the agent build commands below.

The following is the **alternative for a new agent release**, after the
source-controlled number and marker have been bumped and approved.

Use a fresh clone rather than a worktree so Go VCS stamping is unambiguous.
Replace `<commit>` with the approved full commit SHA.

```sh
set -euo pipefail
SRC=/path/to/new-release-src
ART=/path/to/new-release-artifacts
COMMIT=<commit>

[ ! -e "$SRC" ] && [ ! -e "$ART" ]
git clone https://github.com/bokiko/bloxos.git "$SRC"
git -C "$SRC" checkout --detach "$COMMIT"
[ -z "$(git -C "$SRC" status --porcelain)" ]
mkdir -m 700 "$ART"

( cd "$SRC/hub" && go build -o "$ART/bloxos-sign" ./cmd/bloxos-sign )
( cd "$SRC/agent" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$ART/bloxos-agent-linux-amd64" . )
( cd "$SRC/agent" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$ART/bloxos-agent-linux-arm64" . )
( cd "$SRC/agent" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o "$ART/bloxos-agent-windows-amd64.exe" . )

file "$ART/bloxos-sign" "$ART/bloxos-agent-linux-amd64" "$ART/bloxos-agent-linux-arm64" "$ART/bloxos-agent-windows-amd64.exe"
go version -m "$ART/bloxos-agent-linux-amd64"
go version -m "$ART/bloxos-agent-linux-arm64"
go version -m "$ART/bloxos-agent-windows-amd64.exe"
```

Gate for new builds: all three report `vcs.revision=<commit>` and
`vcs.modified=false`; Windows is PE32+ x86-64 and each Linux artifact is the
correct ELF architecture. All carry the same newly approved release number.

## 2. Sign the exact bytes offline

`-key` takes precedence over `BLOXOS_UPDATE_SIGNING_KEY`; when neither is set,
the tool uses `~/.bloxos/update-signing.key`. The tool prints only the target
OS, SHA-256, and signature path — never private key material.

```sh
set -euo pipefail
KEY=/secure/offline/bloxos-update-signing.key
ART=/path/to/approved-release-artifacts

"$ART/bloxos-sign" -key "$KEY" -os linux "$ART/bloxos-agent-linux-amd64"
"$ART/bloxos-sign" -key "$KEY" -os linux "$ART/bloxos-agent-linux-arm64"
"$ART/bloxos-sign" -key "$KEY" -os windows "$ART/bloxos-agent-windows-amd64.exe"
"$ART/bloxos-sign" -key "$KEY" -print-public-key > "$ART/update-signing.pub"

sha256sum \
  "$ART/bloxos-agent-linux-amd64" "$ART/bloxos-agent-linux-amd64.sig" \
  "$ART/bloxos-agent-linux-arm64" "$ART/bloxos-agent-linux-arm64.sig" \
  "$ART/bloxos-agent-windows-amd64.exe" "$ART/bloxos-agent-windows-amd64.exe.sig" \
  "$ART/bloxos-sign" > "$ART/SHA256SUMS"
chmod 0444 "$ART"/*.sig "$ART/update-signing.pub" "$ART/SHA256SUMS"
```

Record the full artifact hashes and public key through a separately trusted
handoff. The private-key file must remain mode `0600` in a non-shared path.

## 3. Stage on the hub

The example paths assume the service is explicitly configured with:

```text
BLOXOS_AGENT_BINARY=/usr/local/lib/bloxos/linux/bloxos-agent
BLOXOS_AGENT_BINARY_ARM64=/usr/local/lib/bloxos/linux/arm64/bloxos-agent
BLOXOS_AGENT_BINARY_WINDOWS=/usr/local/lib/bloxos/windows/bloxos-agent.exe
```

Transfer into a mode-`0700` user-owned transit directory, authenticate the
manifest/hashes received through the trusted handoff, then use `sudo install`
to copy into a root-owned staging directory. Delete only the named transit
files after the root-owned hashes match. The transit directory is never a
signing input. Keep staging outside all active serve directories. Merely
preparing files there does not authorize activation or a fleet update.

Before deployment, assert every ancestor of `/usr/local/lib/bloxos` is
`root:root`, is traversable by the hub service user, and is not group- or
other-writable. Audit unexpected ownership or mode; do not normalize it into a
pass.

## 4. Deploy a signed release

This is a live rollout step and requires its own approval. Keep target agents
offline or rollout-paused according to the release plan.

For each platform, install the **signature first**, then the binary, using
temporary files in the same root-owned serve directory and atomic renames:

```sh
# Example: Linux amd64. Repeat separately for Linux ARM64 and Windows amd64.
set -euo pipefail
STAGE=/usr/local/lib/bloxos/staging
SERVE=/usr/local/lib/bloxos/linux/bloxos-agent

sudo install -o root -g root -m 0644 "$STAGE/bloxos-agent-linux-amd64.sig" "$SERVE.sig.tmp"
sudo install -o root -g root -m 0755 "$STAGE/bloxos-agent-linux-amd64" "$SERVE.tmp"
# Verify both temp-file hashes against the approved manifest here.
sudo mv -f "$SERVE.sig.tmp" "$SERVE.sig"
sudo mv -f "$SERVE.tmp" "$SERVE"
```

Installing the signature first is fail-closed. While it belongs to the next
binary, it cannot verify for the currently served SHA. In hub-held-key mode the
hub can continue signing the current release; in offline mode it withholds an
announcement during that brief mismatch instead of emitting an invalid one.
After the binary rename, check `agent_binaries_by_arch` in `/api/versions`
for the approved SHA of **each** target and confirm `rollout_paused` remains
true with the manual-pause reason. Verify detached signatures against the
existing fleet public key and confirm the native hub is resolving the intended
paths. Resume only through the approved fleet rollout procedure. Keep the old
binary/signature pairs and a pre-change DB backup; do not delete protocol-2
release-floor files to force an older agent rollback.

## 5. One-time keyless cutover — separate operation

Perform this only under a separate reviewed runbook and approval:

1. While the hub still holds the existing private key, sign the **currently
   served** Linux amd64/ARM64 and Windows binaries offline with that same key.
2. Pre-place each matching `.sig` beside its unchanged binary. This is
   operationally a no-op: the hub prefers a detached signature only after it
   verifies for the exact current `(os, sha)`, and Ed25519 signing is
   deterministic. Assert the announced SHAs and signatures are unchanged.
3. Derive the public half with `bloxos-sign -key <offline-key>
   -print-public-key`. Verify it byte-for-byte against the public key already
   pinned across the fleet.
4. Set `BLOXOS_UPDATE_PUBKEY` to that public value and remove/unset
   `BLOXOS_UPDATE_SIGNING_KEY` in the hub service configuration. Restart the
   hub while rollout is controlled, and verify the log reports offline mode,
   the hub holds no private key, all detached signatures verify, and no agent
   sees a changed SHA.
5. Only after those assertions pass, remove the private key from the hub host.
   Preserve independently verified offline backups; losing the sole signing
   key prevents future updates to every pinned agent.

Rollback before step 5 is restoring the previous service configuration and
restarting with the existing key file. After step 5, restore only from the
approved offline backup — never generate a replacement key for an existing
fleet.
