# Offline agent release signing

This runbook builds and signs Linux and Windows agent releases on **FAT-LOLO**,
then installs the binary/signature pairs into root-owned serve directories on
the hub. The private Ed25519 key stays on the offline build host.

> **Scope boundary:** this document describes the later one-time keyless
> cutover, but this PR does not perform it. Do not set
> `BLOXOS_UPDATE_PUBKEY`, move or remove the hub's private key, restart the hub,
> or deploy an agent release without a separate, environment-specific approval.

## Invariants

- Build from a pinned, clean commit and record it with the artifact hashes.
- The signing input and private key remain on FAT-LOLO. Never copy the private
  key to the hub or print it in a terminal transcript.
- `bloxos-sign` and both agents use `updatesigning.Message`; the signed bytes
  are `bloxos-agent-update:v1:<os>:<sha256>` after trimming and lowercasing the
  OS and SHA.
- A detached signature is standard-base64 Ed25519 in `<binary>.sig`, with one
  trailing newline. The hub verifies it against the configured public key
  before announcing it.
- The hub's configured Linux and Windows binary paths must be explicit and
  rooted in a `root:root` chain that is not group- or other-writable. Do not
  rely on relative paths or fallbacks.
- Pause rollout before changing a served artifact. A served SHA change clears
  the current in-memory pause, so verify and re-pause immediately after each
  change before allowing agents to reconnect.

## 1. Build on FAT-LOLO

Use a fresh clone rather than a worktree so Go VCS stamping is unambiguous.
Replace `<commit>` with the approved full commit SHA.

```sh
set -euo pipefail
SRC=/home/bokiko/bloxos-release-src
ART=/home/bokiko/bloxos-release-artifacts
COMMIT=<commit>

[ ! -e "$SRC" ] && [ ! -e "$ART" ]
git clone https://github.com/bokiko/bloxos.git "$SRC"
git -C "$SRC" checkout --detach "$COMMIT"
[ -z "$(git -C "$SRC" status --porcelain)" ]
mkdir -m 700 "$ART"

( cd "$SRC/hub" && go build -o "$ART/bloxos-sign" ./cmd/bloxos-sign )
( cd "$SRC/agent" && go build -o "$ART/bloxos-agent-linux" . )
( cd "$SRC/agent" && GOOS=windows GOARCH=amd64 go build -o "$ART/bloxos-agent-windows.exe" . )

file "$ART/bloxos-sign" "$ART/bloxos-agent-linux" "$ART/bloxos-agent-windows.exe"
go version -m "$ART/bloxos-agent-linux"
go version -m "$ART/bloxos-agent-windows.exe"
```

Gate: both agents report `vcs.revision=<commit>` and `vcs.modified=false`; the
Windows artifact is PE32+ x86-64 and the Linux artifact is the expected ELF.

## 2. Sign on FAT-LOLO

`-key` takes precedence over `BLOXOS_UPDATE_SIGNING_KEY`; when neither is set,
the tool uses `~/.bloxos/update-signing.key`. The tool prints only the target
OS, SHA-256, and signature path — never private key material.

```sh
set -euo pipefail
KEY=/secure/offline/bloxos-update-signing.key
ART=/home/bokiko/bloxos-release-artifacts

"$ART/bloxos-sign" -key "$KEY" -os linux "$ART/bloxos-agent-linux"
"$ART/bloxos-sign" -key "$KEY" -os windows "$ART/bloxos-agent-windows.exe"
"$ART/bloxos-sign" -key "$KEY" -print-public-key > "$ART/update-signing.pub"

sha256sum \
  "$ART/bloxos-agent-linux" "$ART/bloxos-agent-linux.sig" \
  "$ART/bloxos-agent-windows.exe" "$ART/bloxos-agent-windows.exe.sig" \
  "$ART/bloxos-sign" > "$ART/SHA256SUMS"
chmod 0444 "$ART"/*.sig "$ART/update-signing.pub" "$ART/SHA256SUMS"
```

Record the full artifact hashes and public key through a separately trusted
handoff. The private-key file must remain mode `0600` in a non-shared path.

## 3. Stage on the hub

The example paths assume the service is explicitly configured with:

```text
BLOXOS_AGENT_BINARY=/usr/local/lib/bloxos/linux/bloxos-agent
BLOXOS_AGENT_BINARY_WINDOWS=/usr/local/lib/bloxos/windows/bloxos-agent.exe
```

Transfer into a mode-`0700` user-owned transit directory, authenticate the
manifest/hashes received through the trusted handoff, then use `sudo install`
to copy into a root-owned staging directory. Delete only the named transit
files after the root-owned hashes match. The transit directory is never a
signing input.

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
# Example: Linux. Repeat with the Windows names and directory.
set -euo pipefail
STAGE=/usr/local/lib/bloxos/staging
SERVE=/usr/local/lib/bloxos/linux/bloxos-agent

sudo install -o root -g root -m 0644 "$STAGE/bloxos-agent-linux.sig" "$SERVE.sig.tmp"
sudo install -o root -g root -m 0755 "$STAGE/bloxos-agent-linux" "$SERVE.tmp"
# Verify both temp-file hashes against the approved manifest here.
sudo mv -f "$SERVE.sig.tmp" "$SERVE.sig"
sudo mv -f "$SERVE.tmp" "$SERVE"
```

Installing the signature first is fail-closed. While it belongs to the next
binary, it cannot verify for the currently served SHA. In hub-held-key mode the
hub can continue signing the current release; in offline mode it withholds an
announcement during that brief mismatch instead of emitting an invalid one.
After the binary rename, wait for `hub_sha`/`hub_windows_sha` to equal the
approved SHA, then re-pause immediately because the SHA transition clears the
in-memory pause. Resume only through the approved fleet rollout procedure.

## 5. One-time keyless cutover — not part of this PR

Perform this only under a separate reviewed runbook and approval:

1. While the hub still holds the existing private key, sign the **currently
   served** Linux and Windows binaries offline with that same key.
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
   the hub holds no private key, both detached signatures verify, and no agent
   sees a changed SHA.
5. Only after those assertions pass, remove the private key from the hub host.
   Preserve independently verified offline backups; losing the sole signing
   key prevents future updates to every pinned agent.

Rollback before step 5 is restoring the previous service configuration and
restarting with the existing key file. After step 5, restore only from the
approved offline backup — never generate a replacement key for an existing
fleet.
