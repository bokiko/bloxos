# Security Policy

## Reporting a vulnerability

If you believe you have found a security vulnerability in BloxOS, please report
it privately. **Do not open a public GitHub issue.**

Send a description of the issue, reproduction steps, and any proof-of-concept
to **bokiko@users.noreply.github.com**, or use GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository.

You can expect:

- An acknowledgement within **5 business days**.
- A reply with our assessment and intended fix timeline within **15 business
  days**.
- Public disclosure (advisory + patched release) coordinated with you once a
  fix is available.

## Supported versions

Only the current `main` branch receives security fixes. There are no
long-term-support branches yet.

## Scope

In scope:

- The hub HTTP/WebSocket API (`hub/`).
- The agent binary and its installer (`agent/`, `scripts/install.sh`,
  `scripts/install.ps1`).
- The dashboard (`dashboard/`) when served by an unmodified hub.

Out of scope:

- Misconfigurations of operator-controlled infrastructure (Caddy reverse
  proxy, systemd unit overrides, third-party API endpoints).
- Vulnerabilities requiring a pre-compromised admin account or physical
  access to the hub host.

## Hardening notes

- Agents must authenticate with durable, per-machine secrets. Install tokens
  are single-use.
- Terminal sessions require a per-session PIN gate and produce an audit log.
- Database file permissions are enforced at `0600`.
- Browser-side terminal tokens are short-lived (1-minute) and scoped to a
  single session ID.
