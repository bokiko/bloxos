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
# Hub Tests
( cd hub   && go vet ./... && go test -count=1 ./... )

# Hub Tests (race)
( cd hub   && go test -race -count=1 -timeout=10m ./... )

# Agent Tests
( cd agent && go vet ./... && go test -count=1 ./... )
( cd agent && go vet -tags insecure ./... && go test -tags insecure -count=1 ./... )

# Dashboard Lint + Build
( cd dashboard && pnpm install --frozen-lockfile && pnpm lint && pnpm build )
```

The fifth required check, `Agent Tests (windows)`, runs `go vet ./...` and
`go test -count=1 ./...` natively on a Windows runner. A Linux host cannot run
that native job locally. For agent changes that affect Windows, cross-vet as a
precheck before pushing:

```sh
( cd agent && GOOS=windows go vet ./... )
```

All five checks are blocking on `main`. The race and native-Windows jobs cover
behavior that ordinary Linux `go test` does not.

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
- Dashboard: lint + build must pass. There is no test runner yet.

## Reporting bugs / requesting features

Use the issue templates under `.github/ISSUE_TEMPLATE/`. Security issues
follow the separate process in [SECURITY.md](SECURITY.md) — do not file them
publicly.
