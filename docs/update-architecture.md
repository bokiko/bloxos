# Host updater architecture

Developer reference for the shared dashboard/CLI update mechanism. For usage,
see [system updates](system-updates.md).

## Shared contract

- CLI: `sudo bloxos-update update` requests the latest published stable release.
- One-time setup: installed CLI `sudo bloxos-update init` detects supported existing native/systemd or Compose layout, confirms actual paths, installs the host worker and hub mailbox integration. Custom/ambiguous layouts fail with explanation, never switch installations.
- Python stdlib root worker owns transaction and survives hub restart. Fixed root-owned config `/etc/bloxos-updater/config.json`, private state `/var/lib/bloxos-updater/state`, code `/usr/local/lib/bloxos-updater`.
- Mailbox `/var/lib/bloxos-updater/mailbox`: root-owned parent 0755; `inbox` writable by dedicated updater group; `outbox` root-owned, hub-readable not writable. Hub sees host path natively, `/run/bloxos-updater` in Compose, via `BLOXOS_UPDATER_DIR`.
- Request `inbox/request.json`: `{request_id: UUID, target_version: "latest"}` only. No paths, commands, image names or custom URLs in a request. Worker independently resolves a published stable release from bokiko/bloxos and validates manifest.
- `outbox/status.json`: `{request_id, state, version, message, updated_at}`; states idle, checking, staging, backing_up, installing, verifying, succeeded, rolling_back, rolled_back, failed. No private paths, credentials or raw subprocess logs exposed.
- `outbox/capabilities.json`: `{enabled:true, mode:"native"|"compose"}` created by setup. Presence is configuration evidence, not liveness proof.
- `outbox/maintenance`: root-owned marker. New hub blocks application traffic while marker exists, except GET /health, GET /api/build-info and authenticated GET /api/system/update. Existing WebSockets must be quiesced by process stop before backup. Marker remains until accepted or coherent rollback.
- API `GET /api/system/update` returns `{available,reason,current_version,cli_command,status}`. API `POST /api/system/update` accepts only `{"target_version":"latest"}` and writes an exclusive request, returns 202. Both fleet.admin. UI in Settings -> Updates. When absent, explain one-time setup, do not show fake operational button.

## Release manifest (update-manifest.json)

```
{"schema":1,"version":"vX.Y.Z","revision":"40 hex",
 "images":{"hub":"ghcr.io/bokiko/bloxos-hub@sha256:...","dashboard":"ghcr.io/bokiko/bloxos-dashboard@sha256:..."},
 "native":{"amd64":{"file":"bloxos-server-linux-amd64.tar.gz","sha256":"..."},"arm64":{"file":"bloxos-server-linux-arm64.tar.gz","sha256":"..."}}}
```

Native archive contains `hub/bloxos-hub`, dashboard standalone server tree under `dashboard/` including .next/static and public. No private data. Download only fixed GitHub release URLs with verified HTTPS; strict tag/filename/digest validation, bounded downloads, safe extraction. Existing releases without manifest fail clearly before downtime.

## Transaction and adapter interface

`scripts/updater/engine.py`: shared helpers, durable transaction, lock, CLI requests. `scripts/updater/native.py`: NativeAdapter. `scripts/updater/compose.py`: ComposeAdapter.

Adapter constructor `(config, transaction_dir)`; methods:
`preflight()` read-only deployment evidence; `stage(manifest, release_dir)` validate/extract artifacts or pull images before downtime; `quiesce()` stop public proxy THEN hub/dashboard; `backup()` persist original data+artifacts+config while stopped; `install()` switch only selected app artifacts; `start_candidate()` start hub/dashboard and original proxy with maintenance marker; `identities()` returns direct hub/dashboard metadata; `rollback()` stops proxy and components, restores matching snapshot/artifacts/config and restarts original components/proxy; `resume_original()` recovers a failed backup before install without restoring absent snapshots. Adapter journals all original paths/image references/units needed for idempotent rollback in transaction directory BEFORE mutations.

Engine persists phase before each operation. Failure after install invokes rollback; failure during backup resumes untouched original services. On worker restart during mutation, recover durable journal before accepting another request. Never roll back only binary against migrated DB. No user writes between snapshot and acceptance. Public verified TLS identity must equal direct identities and manifest version/revision. Legacy pre-update metadata may be absent; setup must have verified concrete service/proxy ownership rather than claiming metadata verification. Candidate never skips identity proof.

Native standard config fields: mode, public_url, ca_file, mailbox_dir, hub_unit, dashboard_unit, proxy_unit, hub_binary, hub_workdir, hub_home, dashboard_workdir, node_binary, hub_url, dashboard_url. Capture root-controlled systemd drop-ins/external identity paths conservatively; preserve user checkout/untracked files; stage prebuilt bundle, switch dashboard via owned drop-in, keep hub DB working directory. No native agent changes.

Compose standard config fields: mode, public_url, ca_file, mailbox_dir, compose_dir, compose_project, compose_files (absolute ordered list). Require existing single hub/dashboard/caddy and standard persistent volumes; no Docker socket in hub, no orphan cleanup. Write a dedicated updater override with digest-pinned hub/dashboard references; preserve other overrides and volumes. Stop proxy before backup and use whole stopped data directories, not live SQLite copy.

## Validation

Offline transaction, adapter, mailbox and archive tests live in
`scripts/tests/test_updater*.py`. Disposable Linux smoke tests in
`scripts/smoke/updater-compose.py` and `scripts/smoke/updater-native.py` exercise
actual restarts, public identity checks and rollback. Never run smoke fixtures
on an operator's existing server.
