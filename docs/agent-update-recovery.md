# Agent update rollback protection

Normal installation and signing commands do not change. Protocol-2 agents
remember the newest accepted **release number and binary SHA** on their own
machine. They keep running if that state cannot be read or saved, but refuse
self-updates until it is repaired and the agent is restarted.

## Release preparation

Before publishing changed agent bytes, increase the number and matching marker
in `agent/release.go`. Use the same number for Linux amd64, Linux arm64 and
Windows amd64 in that release. Rebuilding with a different compiler, dependencies
or build flags can change the SHA and therefore also requires a new number.
Never reuse a number for different bytes of the same platform.

For local development, use a disposable agent with a separate floor path
(`BLOXOS_RELEASE_FLOOR_PATH`) so test builds never change a real machine's floor.
When changed source, compiler or build settings produce different bytes at the
same number, bump the number or explicitly reset only that disposable agent's
floor using the stopped-service recovery procedure below. Do not disable the
check or reset a deployed agent merely to simplify a development build.

The existing Ed25519 signature authenticates the full SHA, including the embedded
number. The hub does not invent numbers at startup, and agents never advance their
floor from the advisory number in a WebSocket announcement. They inspect the
verified downloaded bytes without executing them.

## Refused updates

The Versions API exposes the agent's running release, floor and floor SHA, plus
the same withholding reason used by the announcement path:

- `agent_release_missing`: serve a numbered agent release.
- `agent_release_below_floor`: serve a release newer than the accepted floor.
- `agent_release_conflict`: different bytes reused a number; build with a higher number.
- `agent_release_floor_unreadable`: repair the agent's local state/permissions and restart it.

The hub does not start a reconnect timer for these refused updates. Agents also
enforce the rules independently, including when connected to an older hub.

## Crash recovery and intentional rollback

The floor is saved durably **before** the executable swap. If staging or swapping
fails after that save, the identical release/SHA can be retried. Other binaries
at that number cannot be substituted.

Linux's existing crash-recovery helper may restore `.prev`; Windows recovery
remains manual. Recovery does not delete or lower the floor. A recovered
protocol-2 agent can keep monitoring normally and accept a later release, or
retry the exact accepted binary. Do not lower the floor merely to fix a failed
download or swap.

For a fleet-wide return to known-good code, rebuild that code with the rollback
protection intact and a **higher** release number, test, and sign it normally.
Re-signing unchanged old bytes cannot change their embedded number.

If a local administrator must intentionally reset the floor, first stop the
agent service, preserve the current executable and floor as backups, and install
an independently verified recovery binary. Move only the floor file aside and
restart the service; a protocol-2 binary seeds a new floor from itself. This is a
deliberate reduction of rollback protection, not a remote hub operation. Preserve
the pinned key, CA, credentials and all unrelated files. The floor is
`/etc/bloxos/agent-release-floor` on Linux and `agent-release-floor` beside the
agent executable on Windows.

## Limits and migration

Existing protocol-1 agents still use their original signature checks for the
migration update. A protocol-2 binary seeds its floor on startup without another
enrollment step. An old protocol-1 `.prev` cannot enforce the new rule if restored;
use a canary and verify recovery during migration. Do not describe legacy agents
as rollback-protected.
A `.prev` restored across the migration boundary remains unenforced until it is
updated or reinstalled to a numbered protocol-2 build.

Rotating the pinned signing key must not reset or namespace the floor. The new
key's releases continue the same numbering. This does not introduce a remote key
rotation mechanism.

Offline signing protects against replay by a hub that cannot forge signatures.
A compromised signer (including a compromised hub holding the private key) can
still sign malicious higher-numbered code. Local administrator access can also
replace the executable or floor. These are not threats this mechanism solves.
