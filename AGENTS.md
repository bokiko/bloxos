# BloxOS

Fleet management dashboard for AI machines. Go hub + agent, Next.js dashboard.

## Architecture
- `hub/` — Go API server (Echo + gorilla/websocket), port 4000
- `agent/` — Go agent binary (gopsutil, go-nvml, creack/pty)
- `dashboard/` — Next.js 16 frontend (React 19, Tailwind, Recharts, xterm.js)
- `proto/` — Shared protocol definitions
- `scripts/` — Install/deploy scripts

## Development
- Hub: `cd hub && go run .`
- Agent: `cd agent && go run . --hub ws://localhost:4000/ws/agent --token <token>`
- Dashboard: `cd dashboard && pnpm dev`

## Rules
- The agent service runs with full privilege on both platforms: **Linux** uses
  `User=root` in the generated systemd unit; **Windows** runs as `LocalSystem`.
  It needs that to collect hardware inventory and manage services.
- Terminal support is **Linux-only**, and the shell it spawns **drops to the
  configured non-root user** — do not confuse the service account with the
  terminal's.
- Terminal sessions require re-auth (server-side PIN gate, bcrypt-verified)
- **Only terminal session metadata is audited** — the `terminal_sessions` row
  records machine, user, source IP, start/end and status. Terminal **content
  and I/O are not recorded**; the hub relays frames without capturing them.
  A full audit log is on the roadmap, not shipped.
- Agent updates carry an Ed25519 release signature. **Protocol-v1** agents
  verify it against their pinned key (Linux: `/etc/bloxos/agent-update.pub`;
  Windows: beside the agent executable) and accept an update only when the
  signature verifies and the transport is permitted. For those agents the hub
  fails closed on missing signature, plaintext transport, or unpinned key.
- **Exception — the legacy migration hop.** Protocol-0 agents cannot verify
  signatures, because verification cannot be retrofitted into an already-running
  binary. They take an unverifiable migration hop to reach protocol v1, and the
  hub permits it only when `PUBLIC_URL` is TLS or loopback. After it, the agent
  is withheld (`agent_key_not_pinned`) until its update key is pinned through a
  trusted provisioning path.
- The signature may come from a detached `<binary>.sig` produced offline, in
  which case the hub holds no private key — do not assume the hub signs.
- **Protocol-2 update rollback protection** keeps the same v1 signature format.
  Its SHA authenticates the release marker inside the binary. Agents persist
  a release number plus SHA floor before replacing the executable, reject
  unnumbered/older builds, and accept the same number only for the same SHA.
  Bump the source-controlled number and marker in `agent/release.go` for every
  new agent release, including rebuilds with changed bytes. Never raise the
  floor from unsigned announcement metadata, reset it on key rotation or
  installer re-runs, or delete it in automatic `.prev` recovery. Protocol-1
  binaries cannot enforce this floor, including a pre-migration `.prev`.
- **AI Sessions is metadata-only monitoring.** Agents report which supported
  AI coding tools (Claude Code, Codex, Kimi) are running, reduced to the
  contract in `proto/aisessions`: opaque id, tool, start time, project
  **basename**, an explicitly-flagged model, and an inferred activity state,
  each with its source and confidence. Never add prompts, responses,
  transcripts, tool commands/output, environment variables, full argv, full
  paths, or the OS username to that contract. The hub keeps live snapshots
  only (no history) and re-sanitizes every frame. The admin switch defaults
  to on; disabling it stops agent-side scanning via the `ai_sessions_config`
  frame, and `BLOXOS_AI_SESSIONS=0` on an agent is a hard local opt-out.
- **Join links** (`GET /join/<token>`, `hub/join.go`) reuse the 15-minute
  install token as the code and serve the verbose Linux bootstrap. They are
  never consumed by a GET — only `enrollment_committed` consumes the token —
  and every unusable code gets the same 404. Behind a private CA the short
  command pins the SPKI of the leaf the hub verified at mint time
  (`curl -k --pinnedpubkey`); the hub refuses to mint if it cannot obtain a
  trustworthy pin. Never emit an unpinned `-k`, a `| bash`, or a
  Host-derived authority, and keep join codes out of logs.
- Credentials NEVER committed — use env vars or local config
- Dark mode is the default UI theme
- Caddy TLS uses RSA-2048 keys, not ECDSA. Do not change without testing on stock Windows PowerShell 5.1.

## Audit conventions
Every code change has two failure surfaces — audit both:
- **Forward audit**: what could break *because of* the change?
- **Backward audit (stale-preamble pass)**: what was relying on the old behavior and is now stranded? Anytime the calling shell, parent context, or invocation model changes (e.g. `iwr | iex` → `powershell.exe -File`, parent-shell env vars → child-process env), code that depended on the old context becomes suspect and needs explicit re-evaluation.
- **Incidental-deletion audit**: when a cleanup step wipes a directory, registry key, or shared resource, enumerate every consumer that reads from it — not just the producer that wrote the thing you're targeting. Wiping `.bloxos/` to remove a stale `agent-secret` may also delete a `ca.crt` that TLS validation silently depends on.
