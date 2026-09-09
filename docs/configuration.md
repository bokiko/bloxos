# Configuration reference

The hub refuses to start unless an origin policy is explicit. Set
`PUBLIC_URL`, `ALLOWED_ORIGINS`, or both before the first boot. Copy
[.env.example](../.env.example) for commented common settings. For Compose,
use [docker/.env.example](../docker/.env.example) instead: the stack derives
the public URL and CA paths from its deployment configuration.

| Variable | Required | Purpose |
|---|---:|---|
| `PUBLIC_URL` | **Yes, unless `ALLOWED_ORIGINS` is set** | Browser-facing hub URL; also drives generated commands and update transport policy. |
| `ALLOWED_ORIGINS` | **Yes, unless `PUBLIC_URL` is set** | Comma-separated browser origins permitted by CORS. |
| `HUB_LISTEN` | No | Hub listen address; defaults to `127.0.0.1:4000`. |
| `BLOXOS_JWT_SECRET` | No | JWT secret, at least 32 bytes; otherwise generated and persisted. |
| `BLOXOS_SETUP_TOKEN` | No | Fixed first-boot setup token; otherwise generated and persisted. |
| `BLOXOS_CA_CERT` | No | Private-CA certificate the hub pins onboarding commands to. Leave **unset** for a publicly trusted (Let's Encrypt, etc.) hub. If set, it is authoritative: a missing, unreadable, or non-PEM file makes the hub refuse to mint install commands rather than fall back. See [TLS trust for onboarding](#tls-trust-for-onboarding). |
| `BLOXOS_PIN_DIAL_ADDR` | No | Internal TLS destination used to verify onboarding pins; Compose sets `caddy:443`. Changes only the TCP target — SNI, hostname, and certificate verification still use `PUBLIC_URL`, so it can never downgrade trust. |
| `BLOXOS_AI_SESSIONS` | Agent | Set to `0` to disable AI-tool metadata collection on that machine. |
| `BLOXOS_AGENT_BINARY` | No | Absolute Linux **amd64** agent binary; blank uses the built-in defaults (`/usr/local/lib/bloxos/linux/amd64/bloxos-agent`, then the legacy `/usr/local/lib/bloxos/linux/bloxos-agent`, then a hub sibling). Served only for the architecture its ELF actually is. |
| `BLOXOS_AGENT_BINARY_ARM64` | No | Absolute Linux arm64 agent binary; blank uses `/usr/local/lib/bloxos/linux/arm64/bloxos-agent`. ELF-verified like all Linux paths, so no request is served another CPU's binary. |
| `BLOXOS_AGENT_BINARY_WINDOWS` | No | Absolute Windows agent binary served by the hub. |
| `BLOXOS_UPDATE_PUBKEY` | No | Base64 Ed25519 public key for detached-signature mode. |
| `BLOXOS_UPDATE_SIGNING_KEY` | No | Explicit online-signing private-key path. |
| `BLOXOS_ALLOW_PRIVATE_TARGETS` | No | Set to `1` to permit API pollers to target RFC1918 addresses. |
| `BLOXOS_TELEGRAM_TOKEN` | No | Telegram bot token; both Telegram values are needed. |
| `BLOXOS_TELEGRAM_CHAT_ID` | No | Telegram destination chat ID. |
| `BLOXOS_HUB` | Agent | Hub base WebSocket URL; the agent appends `/ws/agent`. |
| `BLOXOS_SECRET` | Agent | Durable machine credential, normally managed by enrollment. |
| `BLOXOS_TOKEN` | Agent enrollment | One-time enrollment token. |
| `BLOXOS_TERMINAL_USER` | No | Existing non-root Linux account used for terminal sessions. Unset, the agent tries `bokiko`, `ubuntu`, `admin`; if none exists, or the named account is missing or root, terminals are refused (never run as root). |
| `BLOXOS_UPDATE_PUBKEY_PATH` | No | Override for the agent's pinned update-key file. |
| `BLOXOS_TLS_INSECURE` | Development only | TLS bypass available only in an agent built with `-tags insecure`. |
| `ProgramFiles` | Windows-provided | Used to discover NVIDIA tooling; normally never overridden. |
| `NEXT_PUBLIC_HUB_URL` | Dashboard | Hub origin when dashboard and hub are not same-origin. |

## TLS trust for onboarding

When you generate an Add Machine (or Windows re-enrollment) command, the hub
decides how that command should authenticate its download. Normal installations
need no configuration here:

- **Publicly trusted HTTPS** (Let's Encrypt and the like): leave `BLOXOS_CA_CERT`
  unset. The command is plain verified TLS — no pinning, no `-k`.
- **Private CA** (Caddy's internal CA in the Compose stack): the hub still
  auto-discovers Caddy's local root on the usual paths, verifies the live
  certificate against it, and pins the verified key in the command. This keeps
  working with no extra steps.

Only set `BLOXOS_CA_CERT` when you run your own private CA outside that
auto-discovery. When set it is **authoritative**: if the file is missing,
unreadable, or not a PEM certificate, or the hub's certificate does not verify
against it, the hub refuses to mint a command (an actionable error) instead of
emitting an unverifiable one — fix the path or the certificate and retry.

An unrelated CA left on disk no longer turns a public hub "private": the hub
checks its live certificate against your OS trust store and, if that verifies,
treats the hub as public. If the hub cannot reach `PUBLIC_URL` over TLS at all,
it refuses to mint rather than guess — this is a connectivity problem to fix,
not a trust decision.

For specialized power-history and update-floor settings, see
[power history](power-history.md) and [agent recovery](agent-update-recovery.md).
Do not commit filled environment files or include tokens/private keys in issue reports.
