# Contributing to BloxOS

Thanks for your interest in BloxOS. The repo is currently maintained by a
single operator; PRs are welcome but please open an issue first for anything
larger than a small fix so we can agree on direction before you spend time on
it.

## Code of Conduct

By participating in this project you agree to abide by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

The repo has three components, each runnable independently:

```sh
# Hub (Go API server, port 4000)
cd hub && go run .

# Agent (connects to a running hub)
cd agent && go run . --hub ws://localhost:4000/ws/agent --token <install-token>

# Dashboard (Next.js, port 3000)
cd dashboard && pnpm install && pnpm dev
```

See `AGENTS.md` for repo conventions.

## Pull request gates

Before pushing, run the same checks CI runs:

```sh
( cd hub      && go test -count=1 ./... )
( cd agent    && go test -count=1 ./... )
( cd dashboard && pnpm lint && pnpm build )
```

`go vet ./...` is recommended for Go changes, but it is not currently a CI gate.

For agent changes that affect the Windows build, also run:

```sh
( cd agent && GOOS=windows go vet ./... )
```

## Commit messages

This repo uses Conventional Commits with a multi-target scope when changes
span components:

```
<type>(<scope>): <subject>

<optional body>
```

- `type` — `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
- `scope` — one or more of `hub`, `agent`, `ui` (or `dashboard`), joined with
  `+` when a single change touches several. Examples from the log:
  `feat(hub+agent): Phase 9 — native Windows agent`,
  `fix(dashboard): use server timestamps on SSE reconnect`.
- `subject` — imperative, no trailing period, lowercase after the colon.

Phase-style umbrella commits (e.g. `feat(hub+agent+ui): Phase N — …`) are
reserved for shipped product phases.

## Branch naming

- `feat/<short-topic>`
- `fix/<short-topic>`
- `chore/<short-topic>`
- `docs/<short-topic>`

## What changes need a test

- Hub: new endpoints, bug fixes that have a clear failure surface, security
  changes (auth, RBAC, password/PIN policy). Use the helpers in
  `hub/main_test.go` (`setupTestServer`, `loginAndGetToken`, etc.).
- Agent: bug fixes and safety-sensitive changes should add or update focused
  `_test.go` coverage when the behavior can be exercised without depending on
  real hardware or OS services.
- Dashboard: lint + build must pass. There is no test runner yet.

## Reporting bugs / requesting features

Use the issue templates under `.github/ISSUE_TEMPLATE/`. Security issues
follow the separate process in [SECURITY.md](SECURITY.md) — do not file them
publicly.
