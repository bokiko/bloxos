# Agent versions and rollout

The Versions page (`/versions`) reports what the hub serves and what agents
report. Labels are deliberately literal; this is what each one means.

## Served agent binaries

One card per platform the hub tracks (Linux x86-64, Linux ARM64, Windows
x86-64; older hubs show two unlabeled cards, and the oldest show only the
legacy SHAs).

- **Available / Unavailable** — the hub resolved bytes on a validated path.
  Available is not a trust claim: whether updates are *signed* is a separate,
  hub-wide property shown in the "Update signing" banner above the cards.
- **Release N / Legacy (unnumbered) / Release unknown (older hub)** — the
  release number marker embedded in the served binary (a marker's presence is
  not proof the bytes are signed or verified — that is what the signing banner
  and the agent's own verification cover). `0` means pre-numbering bytes; an
  older hub that does not report the field is shown as unknown, not legacy.
- A platform with no binary (e.g. no Linux ARM64 build) still renders a card
  naming the gap instead of disappearing silently.
- **File modified** is the served file's modification time, not a build date
  or the time the hub last checked it.

## Per-agent status

- **Matches offered build** — the agent's reported SHA equals what the hub
  currently offers for its platform. It does not mean the newest release in
  existence is running; only the hub's current offer is the reference.
- **Version unknown** — the hub has no reported running SHA for this agent
  (older hub response or no report yet), so no match is claimed.
- **Update pending** — the offered SHA differs and nothing blocks the update.
- **Withheld / Unavailable** — the hub refuses to announce (see the blocked
  reason: missing pinned key, unreadable release floor, transport policy,
  or no binary for the platform).
- **Key pinned** — the agent reported a usable pinned update key.
  Protocol-2 agents additionally enforce a signed release floor against
  downgrades; the note **"No rollback floor (protocol 1)"** marks agents that
  verify signatures but do not enforce the floor, and pre-signature agents
  (protocol 0) verify nothing.

## Rollout control

Pause halts update announcements fleet-wide; the automatic circuit breaker
can also pause after repeated rollout failures. See
[update recovery](agent-update-recovery.md) for the failure/rollback paths and
[offline update signing](offline-update-signing.md) for detached-signature
operation.
