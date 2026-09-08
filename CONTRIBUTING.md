# Contributing to BloxOS

Thanks for your interest in BloxOS. The repo is currently maintained by a
single operator; PRs are welcome but please open an issue first for anything
larger than a small fix so we can agree on direction before you spend time on
it.

## Code of Conduct

By participating in this project you agree to abide by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

Run each component in a separate shell, starting from the repository root.
Use a disposable development host, not a machine running a production agent:

```sh
# Hub (Go API server, :4000) — refuses to start without an origin policy
cd hub && PUBLIC_URL=http://localhost:4000 ALLOWED_ORIGINS=http://localhost:3000 go run .

# Dashboard (Next.js, :3000) — must be told the hub origin (cross-origin in dev)
cd dashboard && pnpm install && NEXT_PUBLIC_HUB_URL=http://localhost:4000 pnpm dev

# Agent (connects to a running hub). The --hub flag takes the FULL path
# including /ws/agent; the BLOXOS_HUB env var takes the base and appends it.
cd agent && go run . --hub ws://localhost:4000/ws/agent --token '<install-token>'
```

The full from-source guide — building binaries, serving the agent, the Windows
artifact, the environment reference, and the disposable-agent warning — is in
[docs/development.md](docs/development.md). See [AGENTS.md](AGENTS.md) for repo
conventions and the agent update-safety contract.

## Pull request gates

Before pushing, run the same checks CI runs:

```sh
# Shared Protocol Tests
( cd proto && go vet ./... && go test -race -count=1 ./... )

# Hub Tests
( cd hub   && go vet ./... && go test -count=1 ./... )
python3 scripts/test_backup_compose.py

# Hub Tests (race)   — CI uses -timeout=15m and a 20-minute job cap
( cd hub   && go test -race -count=1 -timeout=15m ./... )

# Agent Tests        — insecure tag, default build, and the race detector
( cd agent && go vet ./... )
( cd agent && go vet -tags insecure ./... && go test -tags insecure -count=1 ./... )
( cd agent && go test -count=1 ./... )
( cd agent && go test -race -count=1 ./... )

# Dashboard Lint + Build   — lint is eslint; test is `node --test src/lib/*.test.mjs`
( cd dashboard && pnpm install --frozen-lockfile && pnpm lint && pnpm test && pnpm build )
```

The `Agent Tests (windows)` job runs natively on a Windows
runner: `go vet ./...`, `go test -count=1 ./...`, and the opt-in service
control manager tests (`BLOXOS_SCM_TEST=1 go test -run 'TestSCM' ./...`, which
install disposable services and need an elevated Windows host). A Linux host
cannot run that native job locally; for agent changes that affect Windows,
cross-vet as a precheck before pushing:

```sh
( cd agent && GOOS=windows go vet ./... )
```

CI has six component jobs. The current branch protection explicitly requires
the hub, hub-race, agent, native-Windows and dashboard checks; shared protocol
tests also run and must be treated as a release gate. Container-related changes
add the Compose smoke job. Use the workflow files as the source of truth for
commands, and check GitHub's branch rules for the current required statuses.

## Merge policy

`main` requires green CI, linear history (squash merges only — no merge
commits), and 1 approving review. Two lanes, depending on who authored the
PR:

- **Dependabot PRs** — approve the diff, then let `dependabot-auto-merge.yml`
  complete the merge once required checks pass. The workflow never bypasses
  protection; it only arms `gh pr merge --auto`.
- **Self-authored PRs** — GitHub blocks approving your own PR, so the review
  requirement has no valid path here. Merge with `gh pr merge --admin
  --squash`. Each use is a deliberate, explicit exception — call it out in the
  PR or session notes, never merge silently past it.

If self-authored PRs start needing `--admin` more than a couple of times a
month, that's the signal to revisit the rule itself (a bot/GitHub App as a
second approver is the fix — not dropping the review requirement, which is
still doing real work gating the Dependabot lane).

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
- Dashboard: lint, `pnpm test`, and build must pass. The test runner is Node's
  built-in `node:test` over `src/lib/*.test.mjs` (pure, framework-free unit
  tests — e.g. `design-prefs`, `design-fleet-model`, `fleet-status`). Add or
  update a focused `*.test.mjs` when a change has testable pure logic; there is
  no component/DOM test harness, so UI wiring is still covered by lint + build
  plus manual browser checks.

## Reporting bugs / requesting features

Use the issue templates under `.github/ISSUE_TEMPLATE/`. Security issues
follow the separate process in [SECURITY.md](SECURITY.md) — do not file them
publicly.
