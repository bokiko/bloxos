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
- **Status unknown (older hub)** — the hub predates per-architecture availability
  reporting. Even if it reports no pending update, the dashboard does not infer
  that the agent matches an available build.
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

In v1.2.1 and later, an operator's **Pause rollout** is saved in the hub database
and survives hub restarts and changes to served agent files. A successful pause
stops new update announcements fleet-wide; it cannot cancel an update already
announced or in progress. Failed pause/resume writes return an error instead of
claiming success, and unreadable saved state withholds announcements.

v1.2.0 and earlier do not enforce the durable pause, including after a
downgrade. See [update recovery](agent-update-recovery.md) for the
failure/rollback paths and [offline update signing](offline-update-signing.md)
for detached-signature operation.

## Automatic staged rollout

When the hub begins serving a new agent binary, it rolls the fleet forward on
its own, per platform (`linux/amd64`, `linux/arm64`, `windows/amd64`). Each
platform progresses independently: one platform halting does not stop another.

**Stages.** One machine first — the canary. Only after it validates does the
next stage open, at two machines at a time. A stage advances only when nothing
is still outstanding in it *and* at least one machine in it actually validated,
so a stage in which every candidate was held back cannot advance on having
proven nothing.

**What validation means.** A machine is validated when it reports running the
new build and then keeps sending telemetry on that same connection,
continuously, for 60 seconds — no gap longer than 45 seconds, and the dwell is
measured between telemetry frames rather than against the clock, so silence
cannot complete it. Reconnecting restarts it: the proof belongs to a
connection, not to a machine.

This is a **liveness** check and nothing more. It says the new agent starts,
stays up, and keeps reporting. It does **not** prove any particular feature
still works. Verify behaviour you care about yourself.

**Pause versus halt.** These are different and the Versions page names them
separately:

| | Operator pause | Platform halt |
|---|---|---|
| Set by | a person, via **Pause rollout** | the hub, when an attempt fails |
| Scope | the whole fleet | one platform |
| Survives restart | yes | yes |
| Cleared by | **Resume** | **Retry halted rollouts** (the same action) |

A halt does **not** set the operator pause. A halted platform with the pause
off is not "paused" — it is stopped and waiting for a person.

**Retrying.** One action covers both: it clears the operator pause and gives
every halted platform a fresh attempt, in one transaction — all of it or none.
It is fleet-wide; there is no per-platform retry. A failed attempt is otherwise
terminal, so nothing moves until you use it.

**Machines that were offline.** A machine that was not connected during a
stage is not skipped. It has no slot, is not counted, and is picked up by a
later pass on capacity alone — no reconnect, version change, or operator action
needed. This is also why the counts on the Versions page describe the machines
this rollout reserved a slot for, not the fleet: a machine already on the build
never needed one.

**Which build the counts describe.** Each platform row names the candidate SHA
it is tracking. That is not always what the hub serves right now — a new
candidate is only picked up when the rollout next reserves a slot, so while the
pause is on the hub can serve one build while the rollout still describes the
previous one. Compare the row's build against **Served binaries** before
reading the counts.

**A hub update is not a fleet update.** `bloxos-update` finishing successfully
means the server is on the new release. The agents are not: they roll out
afterwards, over stages, and a halted or paused platform may never get there.
The Versions page is where that is answered.
