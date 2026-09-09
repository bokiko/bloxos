# Verify the installation you are updating

An upgrade is not complete just because containers started or `/health`
returns `ok`. An older native installation may still serve your website.
Likewise, the Git checkout tag does not identify an already running executable.

## Current scope

The [host updater](system-updates.md) uses these checks after staging and
installing a release. The script below remains a **read-only verification**
tool; running it does not install anything. Do not use Docker commands to
update a native installation, and do not switch databases or proxy upstreams
as an upgrade shortcut.

The new endpoints are:

| Component | Public path | Reports |
| --- | --- | --- |
| Hub | `/api/build-info` | Hub executable version, revision, process instance ID |
| Dashboard | `/build-info` | Compiled dashboard version, revision, process instance ID |

These unauthenticated endpoints contain no secrets or filesystem paths and
disable caching. The instance ID changes on restart, distinguishing two
processes running the same release. It is a correlation identifier, not proof
of authenticity; TLS and the trusted local connection provide that boundary.
Existing proxy configurations forwarding `/api/*` to the hub and other paths
to the dashboard need no new route.

`/health` remains a liveness check. Existing `hub_sha` fields in the agent
versions response still describe a **served agent payload**, not the hub build.

## Read-only check

For a native installation with verified local listeners on ports 4000/3000:

```sh
python3 scripts/upgrade_preflight.py \
  --public-url https://YOUR-HUB \
  --local-hub-url http://127.0.0.1:4000 \
  --local-dashboard-url http://127.0.0.1:3000 \
  --detect
```

For private TLS, add `--ca-file /path/to/the/trusted/root.crt`. Obtain that CA
from your known installation, not an unverified download. The check never
disables certificate verification. Use the actual ports; local URLs must be
known to belong to the installation you intend to update.

Add `--expect-version vX.Y.Z --expect-revision FULL_COMMIT_SHA` to verify a
particular release. Public and local identities must agree for **both**
components. Missing metadata in an older version is **UNKNOWN**, not success
and not proof that the website is unhealthy. Native source builds without
build stamps are also unverifiable as releases.

This read-only script is not an automatic deployment detector: unit/container observations
are evidence, not proof of proxy routing. A native Caddy process can proxy
containers. Missing Docker permissions are not evidence that no containers
exist. Load-balanced replicas require a replica-aware workflow; a single
instance comparison intentionally refuses an ambiguous result.

## Preserve the original installation

- Do not remove orphan containers, volumes, untracked files or old keys as an
  upgrade step. They may contain the real database or signing identity.
- Keep a consistent backup, including SQLite WAL and all applicable identity
  files. The existing Compose backup helper is **not** a native backup tool.
- Build and validate both replacement components before downtime.
- Do not restore only an old executable after a database migration; rollback
  must account for the matching database snapshot and writes since the backup.
- Hub/dashboard verification does not claim the fleet agents were updated.
  Native agent payloads and offline signatures remain separate operations.

## Build stamps

Release images stamp both components with the release ref and source revision.
For deliberate native builds, stamp the hub at link time:

```sh
go build -ldflags "-X main.buildVersion=vX.Y.Z -X main.buildRevision=FULL_COMMIT_SHA" .
```

Build the dashboard with `BLOXOS_BUILD_VERSION` and `BLOXOS_BUILD_REVISION` set
to the same values. These values are compiled into the dashboard; changing
its runtime environment cannot relabel an old build. These are build examples,
not deployment commands. Agent binaries are deliberately not restamped.
