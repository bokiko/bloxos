#!/usr/bin/env python3
"""Durable root-owned update transaction engine (stdlib only).

The engine drives a deployment-agnostic upgrade through a fixed phase machine,
persisting each phase to a journal BEFORE the operation so an interrupted
worker recovers coherently (resume the untouched original, or roll back a
partial switch). It never trusts the hub for anything but a version request:
the release manifest and artifacts are resolved and verified here, from the
fixed GitHub repository, over verified HTTPS. A deployment adapter (native or
compose) performs the privileged, layout-specific steps behind a fixed method
contract; the engine owns ordering, journaling, status and verification.

No module-level side effects. Network and identity lookups are injected so the
tests are fully offline; production defaults use urllib with TLS verification.
"""
from __future__ import annotations

import contextlib
import dataclasses
import fcntl
import hashlib
import json
import os
import re
import shutil
import ssl
import tarfile
import time
import urllib.request
import uuid

GITHUB_REPO = "bokiko/bloxos"
RELEASE_LATEST = f"https://github.com/{GITHUB_REPO}/releases/latest/download/update-manifest.json"
RELEASE_ASSET = f"https://github.com/{GITHUB_REPO}/releases/download/{{version}}/{{file}}"
ALLOWED_DOWNLOAD_HOSTS = ("github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com")

MAX_MANIFEST = 64 * 1024
MAX_ARCHIVE = 512 * 1024 * 1024
VERSION_RE = re.compile(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?\Z")
SHA_RE = re.compile(r"[0-9a-f]{64}\Z")
REV_RE = re.compile(r"[0-9a-f]{40}\Z")
FILENAME_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]*\.tar\.gz\Z")
FIELDS = ("component", "version", "revision", "instance_id")
DEV_MARKERS = {"", "dev", "development", "unknown", "unreleased", "snapshot"}

# Phase machine. Order matters: recovery decides resume-vs-rollback from the
# last durably-recorded phase, and INSTALL is the point of no cheap return.
CHECKING = "checking"
STAGING = "staging"
BACKING_UP = "backing_up"
INSTALLING = "installing"
VERIFYING = "verifying"
SUCCEEDED = "succeeded"
ROLLING_BACK = "rolling_back"
ROLLED_BACK = "rolled_back"
FAILED = "failed"
IDLE = "idle"

# Phases at or after which the original artifacts may already be replaced, so a
# failure must roll back rather than merely restart the original.
_MUTATING = (INSTALLING, VERIFYING)


class UpdaterError(Exception):
    """A clean, operator-facing failure. Message carries no secrets/paths.
    `fatal` marks a decisive failure that readiness retries must not swallow."""
    fatal = False


def _fatal(message: str) -> UpdaterError:
    err = UpdaterError(message)
    err.fatal = True
    return err


@dataclasses.dataclass
class Config:
    mode: str
    public_url: str
    ca_file: str | None
    mailbox_dir: str
    state_dir: str
    # Native fields (compose adapter reads its own subset).
    extra: dict

    @classmethod
    def from_dict(cls, d: dict) -> "Config":
        for key in ("mode", "public_url", "mailbox_dir", "state_dir"):
            if not isinstance(d.get(key), str) or not d[key]:
                raise UpdaterError(f"config missing required field: {key}")
        if d["mode"] not in ("native", "compose"):
            raise UpdaterError("config mode must be native or compose")
        known = {"mode", "public_url", "ca_file", "mailbox_dir", "state_dir"}
        return cls(
            mode=d["mode"], public_url=d["public_url"], ca_file=d.get("ca_file") or None,
            mailbox_dir=d["mailbox_dir"], state_dir=d["state_dir"],
            extra={k: v for k, v in d.items() if k not in known},
        )

    @classmethod
    def load(cls, path: str, require_secure: bool = True) -> "Config":
        import stat
        try:
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        except OSError as err:
            raise UpdaterError(f"cannot open updater config: {type(err).__name__}")
        try:
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode):
                raise UpdaterError("updater config is not a regular file")
            if require_secure:
                # Owned by the running (root) user and not writable by group or
                # other: a web-writable config would be a privilege hole.
                if info.st_uid != os.geteuid():
                    raise UpdaterError("updater config is not owned by the updater user")
                if info.st_mode & 0o022:
                    raise UpdaterError("updater config is group/world-writable")
            if info.st_size > MAX_MANIFEST:
                raise UpdaterError("updater config is implausibly large")
            raw = os.read(fd, MAX_MANIFEST + 1)
        finally:
            os.close(fd)
        try:
            return cls.from_dict(json.loads(raw))
        except ValueError:
            raise UpdaterError("updater config is not valid JSON")


class Mailbox:
    """Filesystem request/status boundary between the hub and the worker.

    The hub only ever WRITES inbox/request.json and READS outbox/*. Status and
    capabilities are sanitized (no paths, credentials or raw logs)."""

    def __init__(self, mailbox_dir: str):
        self.dir = mailbox_dir
        self.inbox = os.path.join(mailbox_dir, "inbox")
        self.outbox = os.path.join(mailbox_dir, "outbox")
        self.request_path = os.path.join(self.inbox, "request.json")
        self.status_path = os.path.join(self.outbox, "status.json")
        self.capabilities_path = os.path.join(self.outbox, "capabilities.json")
        self.maintenance_path = os.path.join(self.outbox, "maintenance")

    def ensure(self):
        os.makedirs(self.inbox, exist_ok=True)
        os.makedirs(self.outbox, exist_ok=True)

    def read_request(self) -> dict | None:
        # The inbox is writable by the hub group, so the request is untrusted:
        # no symlink following, a regular file only, bounded, strict schema.
        try:
            raw = _read_regular_bounded(self.request_path, MAX_MANIFEST)
        except FileNotFoundError:
            return None
        except UpdaterError as err:
            raise UpdaterError(f"invalid request file: {err}")
        except OSError as err:
            raise UpdaterError(f"cannot read request: {type(err).__name__}")
        if len(raw) > MAX_MANIFEST:
            raise UpdaterError("request is implausibly large")
        try:
            req = json.loads(raw)
        except ValueError:
            raise UpdaterError("request is not valid JSON")
        if not isinstance(req, dict):
            raise UpdaterError("request must be a JSON object")
        extras = set(req) - {"request_id", "target_version"}
        if extras:
            raise UpdaterError("request carries unexpected fields")
        rid = req.get("request_id")
        if not isinstance(rid, str) or not _is_uuid(rid):
            raise UpdaterError("request_id must be a UUID")
        if req.get("target_version") != "latest":
            raise UpdaterError("target_version must be exactly 'latest'")
        return {"request_id": rid, "target_version": "latest"}

    def consume_request(self):
        with contextlib.suppress(FileNotFoundError):
            os.remove(self.request_path)

    def write_status(self, state, *, version="", message="", request_id=""):
        payload = {
            "request_id": request_id, "state": state, "version": version,
            "message": message, "updated_at": _utc_now(),
        }
        _atomic_write_json(self.status_path, payload, mode=OUTBOX_MODE)

    def write_capabilities(self, mode):
        _atomic_write_json(self.capabilities_path, {"enabled": True, "mode": mode}, mode=OUTBOX_MODE)

    def set_maintenance(self):
        os.makedirs(self.outbox, exist_ok=True)
        tmp = self.maintenance_path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(_utc_now() + "\n")
            fh.flush()
            os.fsync(fh.fileno())
        os.chmod(tmp, OUTBOX_MODE)
        os.replace(tmp, self.maintenance_path)
        _fsync_parent(self.maintenance_path)

    def clear_maintenance(self):
        with contextlib.suppress(FileNotFoundError):
            os.remove(self.maintenance_path)
            _fsync_parent(self.maintenance_path)

    def maintenance_present(self) -> bool:
        return os.path.exists(self.maintenance_path)


@contextlib.contextmanager
def exclusive_lock(path: str):
    """Single-worker guard. A held lock means a transaction is in flight;
    callers must not start a second one."""
    fd = os.open(path, os.O_CREAT | os.O_RDWR, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            raise UpdaterError("another update is already in progress")
        yield
    finally:
        with contextlib.suppress(OSError):
            fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)


class Journal:
    """Durable phase + adapter facts, fsync'd, so an interrupted worker recovers
    the exact point reached before the crash."""

    def __init__(self, transaction_dir: str):
        self.dir = transaction_dir
        self.path = os.path.join(transaction_dir, "journal.json")

    def _load(self) -> dict:
        try:
            with open(self.path, "rb") as fh:
                return json.loads(fh.read())
        except (FileNotFoundError, ValueError):
            return {}

    def set_phase(self, phase: str):
        data = self._load()
        data["phase"] = phase
        _atomic_write_json(self.path, data)

    def phase(self) -> str | None:
        return self._load().get("phase")

    def record(self, key: str, value):
        data = self._load()
        data.setdefault("facts", {})[key] = value
        _atomic_write_json(self.path, data)

    def get(self, key, default=None):
        return self._load().get("facts", {}).get(key, default)

    def exists(self) -> bool:
        return os.path.exists(self.path)

    def clear(self):
        with contextlib.suppress(FileNotFoundError):
            os.remove(self.path)


# ----- release resolution & artifact safety -------------------------------

def default_fetch_bytes(url: str, max_bytes: int, ca_file: str | None = None,
                        timeout: float = 30.0) -> bytes:
    """Fetch a fixed release URL over verified HTTPS, bounded. Redirects to
    GitHub's asset CDN are followed by urllib; the initial and final hosts are
    allowlisted, and every artifact is checksum-verified by the caller, so a
    misrouted body cannot be accepted."""
    context = ssl.create_default_context(cafile=ca_file) if ca_file else ssl.create_default_context()
    start = _split(url)
    if start.scheme != "https" or start.hostname not in ALLOWED_DOWNLOAD_HOSTS:
        raise UpdaterError("refusing a non-allowlisted download location")
    req = urllib.request.Request(url, headers={"Accept": "application/octet-stream"})
    try:
        with _no_proxy_opener(context).open(req, timeout=timeout) as resp:
            final = _split(resp.geturl())
            if final.scheme != "https" or final.hostname not in ALLOWED_DOWNLOAD_HOSTS:
                raise UpdaterError("download redirected to a non-allowlisted host")
            body = resp.read(max_bytes + 1)
    except UpdaterError:
        raise
    except OSError as err:
        raise UpdaterError(f"download failed: {type(err).__name__}")
    if len(body) > max_bytes:
        raise UpdaterError("download exceeded its size bound")
    return body


def resolve_latest_manifest(fetch_bytes) -> dict:
    raw = fetch_bytes(RELEASE_LATEST, MAX_MANIFEST)
    return validate_manifest(raw)


def validate_manifest(raw: bytes) -> dict:
    try:
        obj = json.loads(raw)
    except ValueError:
        raise UpdaterError("release manifest is not valid JSON; this release predates verified updates")
    if not isinstance(obj, dict) or obj.get("schema") != 1:
        raise UpdaterError("unsupported release manifest schema")
    version = obj.get("version")
    revision = obj.get("revision")
    if not isinstance(version, str) or not VERSION_RE.fullmatch(version):
        raise UpdaterError("manifest version is not a valid release tag")
    if not isinstance(revision, str) or not REV_RE.fullmatch(revision):
        raise UpdaterError("manifest revision is not a 40-hex commit SHA")
    native = obj.get("native")
    if not isinstance(native, dict) or not native:
        raise UpdaterError("manifest has no native artifacts")
    clean_native = {}
    for arch, entry in native.items():
        if arch not in ("amd64", "arm64"):
            raise UpdaterError("manifest names an unsupported architecture")
        if not isinstance(entry, dict):
            raise UpdaterError("manifest native entry is malformed")
        fname, sha = entry.get("file"), entry.get("sha256")
        if not isinstance(fname, str) or "/" in fname or "\\" in fname or not fname.endswith(".tar.gz"):
            raise UpdaterError("manifest native filename is unsafe")
        if not isinstance(sha, str) or not SHA_RE.fullmatch(sha):
            raise UpdaterError("manifest native sha256 is invalid")
        clean_native[arch] = {"file": fname, "sha256": sha}
    images = obj.get("images")
    if not isinstance(images, dict):
        images = {}
    return {"schema": 1, "version": version, "revision": revision,
            "images": images, "native": clean_native}


def download_and_verify(url: str, dest: str, sha256_hex: str, fetch_bytes, max_bytes=MAX_ARCHIVE):
    data = fetch_bytes(url, max_bytes)
    got = hashlib.sha256(data).hexdigest()
    if got != sha256_hex:
        raise UpdaterError("downloaded artifact failed its manifest checksum")
    tmp = dest + ".part"
    with open(tmp, "wb") as fh:
        fh.write(data)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, dest)
    _fsync_parent(dest)


def safe_extract_tar(archive_path: str, dest_dir: str):
    """Extract files first, then safe relative links needed by pnpm/Node.

    No file is written through an archive-created link. Final link chains must
    resolve inside the fresh root before the adapter may publish this tree.
    Archive ownership and setuid/setgid bits are never applied.
    """
    from pathlib import Path, PurePosixPath
    root = Path(dest_dir)
    if root.is_symlink():
        raise UpdaterError("extraction root is a symlink")
    root.mkdir(parents=True, exist_ok=True)
    if any(root.iterdir()):
        raise UpdaterError("extraction root must be empty")
    root = root.resolve()
    with tarfile.open(archive_path, "r:gz") as tar:
        members = tar.getmembers()
        if len(members) > 100000 or sum(m.size for m in members) > 2 * 1024 ** 3:
            raise UpdaterError("expanded archive exceeds its bound")
        names, links = set(), set()
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not path.parts:
                raise UpdaterError("archive member escapes the extraction root")
            name = str(path)
            if name in names:
                raise UpdaterError("duplicate archive member")
            names.add(name)
            if member.issym():
                if not member.linkname or PurePosixPath(member.linkname).is_absolute():
                    raise UpdaterError("archive has an absolute or empty symlink")
                links.add(name)
            elif not (member.isfile() or member.isdir()):
                raise UpdaterError("archive contains a non-regular member")
        for member in members:
            path = PurePosixPath(member.name)
            if any(str(parent) in links for parent in path.parents):
                raise UpdaterError("archive member is beneath a symlink")
            destination = root.joinpath(*path.parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            if member.isdir():
                destination.mkdir(exist_ok=True)
            elif member.isfile():
                with tar.extractfile(member) as source, destination.open("xb") as output:
                    shutil.copyfileobj(source, output)
                    output.flush()
                    os.fsync(output.fileno())
                os.chmod(destination, member.mode & 0o755)
        for member in members:
            if member.issym():
                (root / member.name).symlink_to(member.linkname)
        for name in links:
            try:
                try:
                    resolved = (root / name).resolve(strict=True)
                except FileNotFoundError:
                    resolved = (root / name).resolve(strict=False)
            except (OSError, RuntimeError) as error:
                raise UpdaterError("archive contains an invalid symlink chain") from error
            if resolved != root and root not in resolved.parents:
                raise UpdaterError("archive symlink resolves outside extraction root")
        # Undo only the root worker's private umask for the installable code
        # directories; private transaction/backup parents remain root-only.
        for directory, _, _ in os.walk(root, followlinks=False):
            os.chmod(directory, 0o755)
            _fsync_parent(os.path.join(directory, "."))


# ----- identity verification ----------------------------------------------

def default_identity_fetch(base_url: str, component: str, ca_file: str | None, timeout: float) -> dict:
    """Fetch a build-info endpoint (no redirects, no-store required) and return
    its validated identity. Raises UpdaterError with a coarse reason."""
    path = "api/build-info" if component == "hub" else "build-info"
    url = base_url.rstrip("/") + "/" + path
    parts = _split(url)
    if parts.scheme not in ("http", "https"):
        raise UpdaterError("identity URL must be http(s)")
    # Augment the system trust store with the private CA (if any) rather than
    # replacing it, so a publicly trusted hub still verifies even when a private
    # BLOXOS_CA_CERT is configured/left over.
    context = ssl.create_default_context()
    if ca_file:
        context.load_verify_locations(cafile=ca_file)
    opener = urllib.request.build_opener(_NoRedirect(), urllib.request.HTTPSHandler(context=context),
                                         urllib.request.ProxyHandler({}))
    try:
        with opener.open(urllib.request.Request(url, headers={"Accept": "application/json"}), timeout=timeout) as resp:
            headers = resp.headers
            body = resp.read(MAX_MANIFEST + 1)
    except OSError as err:
        raise UpdaterError(f"identity endpoint unreachable: {type(err).__name__}")
    if len(body) > MAX_MANIFEST:
        raise UpdaterError("identity response too large")
    if "no-store" not in headers.get("Cache-Control", ""):
        raise UpdaterError("identity endpoint missing no-store; not verifiable")
    try:
        data = json.loads(body)
    except ValueError:
        raise UpdaterError("identity endpoint returned non-JSON; legacy/unverifiable")
    if not isinstance(data, dict):
        raise UpdaterError("identity is not an object")
    for field in FIELDS:
        if not isinstance(data.get(field), str) or not data[field].strip():
            raise UpdaterError("identity missing required fields; legacy/unverifiable")
    if data["component"] != component:
        raise UpdaterError("identity reports the wrong component (proxy misroute)")
    return data


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise UpdaterError("identity endpoint attempted a redirect")


# ----- the transaction ------------------------------------------------------

class Transaction:
    def __init__(self, config: Config, adapter, *, fetch_bytes=None,
                 identity_fetch=None, now=None, readiness_timeout=120.0,
                 poll_interval=3.0, sleep=None, monotonic=None):
        self.config = config
        self.adapter = adapter
        self.mailbox = Mailbox(config.mailbox_dir)
        self.transaction_dir = os.path.join(config.state_dir, "transaction")
        self.journal = Journal(self.transaction_dir)
        # Release downloads use the SYSTEM trust store only — GitHub is public.
        # config.ca_file is solely for the hub's own (possibly private-CA) app
        # endpoint identity check, never for fetching artifacts.
        self._fetch = fetch_bytes or (lambda url, mx: default_fetch_bytes(url, mx))
        self._identity = identity_fetch or (
            lambda base, comp: default_identity_fetch(base, comp, config.ca_file, 5.0))
        self._now = now or _utc_now
        self.readiness_timeout = readiness_timeout
        self.poll_interval = poll_interval
        self._sleep = sleep or time.sleep
        self._monotonic = monotonic or time.monotonic

    # apply drives a fresh request through the full phase machine. Pre-downtime
    # failures (checking/preflight/staging) end in a clean FAILED with nothing
    # switched; a backup/quiesce failure resumes the untouched original; any
    # install/verify failure rolls back.
    def apply(self, request_id: str, target_version: str = "latest") -> str:
        if target_version != "latest":
            raise UpdaterError("only 'latest' is supported")
        os.makedirs(self.transaction_dir, exist_ok=True)
        self.mailbox.ensure()
        rid = request_id
        version = ""

        def status(state, version="", message=""):
            self.mailbox.write_status(state, version=version, message=message, request_id=rid)

        # --- pre-downtime: nothing live changes; any failure is a clean fail.
        try:
            status(CHECKING)
            self.journal.set_phase(CHECKING)
            self.journal.record("request_id", rid)  # so recovery can report it
            manifest = resolve_latest_manifest(self._fetch)
            version = manifest["version"]
            self.journal.record("version", version)
            self.adapter.preflight()  # ownership evidence; fails on custom/mixed

            status(STAGING, version)
            self.journal.set_phase(STAGING)
            release_dir = os.path.join(self.transaction_dir, "release")
            os.makedirs(release_dir, exist_ok=True)
            self.adapter.stage(manifest, release_dir)
        except Exception as err:
            return self._fail(rid, version, self._reason(err, "preparation failed before any downtime"))

        # --- downtime begins: quiesce + backup. Failure here never leaves a
        #     half-switched host: bring the original services back, no restore.
        status(BACKING_UP, version)
        self.journal.set_phase(BACKING_UP)
        try:
            self.adapter.quiesce()  # stop public proxy THEN hub/dashboard
            self.adapter.backup()
        except Exception as err:
            try:
                self.adapter.resume_original()
            except Exception as reserr:
                # Services may be stopped and we could NOT restart them. Do not
                # destroy the journal or consume the request: keep the phase
                # pre-install (recoverable via resume_original) and say so, so a
                # later worker run retries recovery instead of leaving the host
                # permanently down with no trace.
                self.journal.set_phase(BACKING_UP)
                self.mailbox.write_status(
                    FAILED, version=version, request_id=rid,
                    message="backup failed and original services could not be restarted; "
                            "manual recovery required (journal retained): "
                            + self._reason(reserr, "restart error"))
                return FAILED
            return self._finish(FAILED, rid, version, self._reason(err, "backup failed before install"))

        # --- install + verify: the mutating region; any failure rolls back.
        status(INSTALLING, version)
        self.journal.set_phase(INSTALLING)
        self.mailbox.set_maintenance()  # the NEW hub gates writes the moment it serves
        try:
            self.adapter.install()
            status(VERIFYING, version)
            self.journal.set_phase(VERIFYING)
            self.adapter.start_candidate()
            self._verify(manifest)
        except Exception as err:
            return self._rollback(rid, version, self._reason(err, "install or verification failed"))

        # --- accept: durably record the commit BEFORE reopening traffic. A
        #     crash after this line recovers as SUCCEEDED (finalize), never a
        #     rollback that would discard writes accepted once traffic resumed.
        return self._finish(SUCCEEDED, rid, version, "update verified on the public endpoint")

    def _finish(self, state, rid, version, message):
        # Legacy originals cannot read the maintenance marker. Keep the public
        # proxy closed until rollback is DURABLY committed; a second crash must
        # never restore a snapshot over writes accepted after rollback.
        self.journal.set_phase(state)
        try:
            finalize = getattr(self.adapter, "finalize", None)
            if finalize:
                finalize()
            self.mailbox.clear_maintenance()
        except Exception as err:
            self.mailbox.write_status(FAILED, version=version, request_id=rid,
                message="release state committed but reopening failed; retry recovery (journal retained): "
                        + self._reason(err, "finalization error"))
            return FAILED
        self.mailbox.consume_request()
        self.mailbox.write_status(state, version=version, message=_short(message), request_id=rid)
        self.journal.clear()
        return state

    def _verify(self, manifest):
        # Bounded readiness: a just-restarted candidate (systemctl start /
        # compose up) needs time before it answers and before the proxy points
        # at it. Retry until the deadline; a decisive build mismatch fails fast.
        deadline = self._monotonic() + self.readiness_timeout
        while True:
            try:
                self._verify_once(manifest)
                return
            except UpdaterError as err:
                if getattr(err, "fatal", False) or self._monotonic() >= deadline:
                    raise
                self._sleep(self.poll_interval)
            except (RuntimeError, OSError):
                # A transient readiness failure from an adapter identity probe
                # (e.g. wget against a not-yet-listening candidate raising
                # RuntimeError) — retry until the deadline. Never a pass: an
                # endpoint that never becomes verifiable exhausts and fails.
                if self._monotonic() >= deadline:
                    raise UpdaterError("candidate did not become verifiable within the readiness window")
                self._sleep(self.poll_interval)

    def _verify_once(self, manifest):
        direct = self.adapter.identities()  # {"hub": {...}, "dashboard": {...}}
        want_v, want_r = manifest["version"], manifest["revision"]
        for comp in ("hub", "dashboard"):
            d = direct.get(comp)
            if not isinstance(d, dict) or not d.get("instance_id"):
                raise UpdaterError("candidate not ready: no direct identity yet")
            if d.get("version") != want_v or d.get("revision") != want_r:
                # The started artifact IS the candidate; a mismatch is decisive.
                raise _fatal("candidate build does not match the manifest")
            pub = self._identity(self.config.public_url, comp)  # may raise (not ready) -> retry
            if pub["version"] != want_v or pub["revision"] != want_r:
                raise UpdaterError("public endpoint does not yet serve the new build")
            if pub["instance_id"] != d["instance_id"]:
                raise UpdaterError("public route does not yet reach the started candidate")

    @staticmethod
    def _reason(err, fallback):
        return str(err) if isinstance(err, UpdaterError) else fallback

    def _rollback(self, rid, version, message) -> str:
        self.mailbox.write_status(ROLLING_BACK, version=version, message="restoring the previous release", request_id=rid)
        self.journal.set_phase(ROLLING_BACK)
        try:
            self.adapter.rollback()
        except Exception as err:
            # A rollback that itself fails is the worst case: services may be
            # down and the state half-restored. Do NOT claim success or clear
            # the journal — leave it (and the retained backup) for recovery and
            # say so loudly. Keep the maintenance marker on.
            self.mailbox.write_status(
                FAILED, version=version, request_id=rid,
                message="automatic rollback failed; manual recovery required (backup retained): "
                        + self._reason(err, "rollback error"))
            return FAILED
        # Durably record ROLLED_BACK BEFORE clearing the marker, so a crash
        # here recovers as a completed rollback (finalize), never re-rolls.
        return self._finish(ROLLED_BACK, rid, version, message)

    def _fail(self, rid, version, message) -> str:
        self.journal.set_phase(FAILED)
        self.journal.clear()
        self.mailbox.consume_request()
        self.mailbox.write_status(FAILED, version=version, message=_short(message), request_id=rid)
        return FAILED

    # recover runs at worker startup: an existing journal means a prior run was
    # interrupted mid-mutation and must be resolved before any new request.
    def recover(self) -> str:
        if not self.journal.exists():
            return IDLE
        phase = self.journal.phase()
        rid = self.journal.get("request_id", "")
        version = self.journal.get("version", "")
        if phase in (SUCCEEDED, ROLLED_BACK, FAILED):
            # Finalization is idempotent. Never restore a committed snapshot
            # again, even if traffic reopened just before a second power loss.
            return self._finish(phase, rid, version, "recovered a committed transaction")
        if phase in _MUTATING or phase == ROLLING_BACK:
            try:
                self.adapter.rollback()
            except Exception as err:
                # Do not clear the marker/journal or claim success: leave the
                # journal (and retained backup) for another recovery attempt.
                self.mailbox.write_status(
                    FAILED, version=version, request_id=rid,
                    message="automatic rollback failed during recovery; manual recovery required "
                            "(journal retained): " + self._reason(err, "rollback error"))
                return FAILED
            # Durably record ROLLED_BACK BEFORE clearing the marker so a crash
            # here re-enters as a completed rollback, never re-rolls or loses
            # accepted writes.
            return self._finish(ROLLED_BACK, rid, version, "recovered an interrupted update")
        # Pre-install interruption: originals may be stopped but never replaced.
        try:
            self.adapter.resume_original()
        except Exception as err:
            # Could not restart the originals: keep the journal for a retry.
            self.mailbox.write_status(
                FAILED, version=version, request_id=rid,
                message="could not restart original services during recovery; manual recovery "
                        "required (journal retained): " + self._reason(err, "restart error"))
            return FAILED
        return self._finish(FAILED, rid, version, "recovered an interrupted update before install")


# ----- CLI / worker entry points -------------------------------------------

def write_update_request(config: Config, target_version: str = "latest") -> str:
    """CLI/hub side: enqueue exactly one update request (fixed schema, fresh
    UUID) atomically. Refuses if a request is already pending. Returns the id."""
    if target_version != "latest":
        raise UpdaterError("only 'latest' may be requested")
    mailbox = Mailbox(config.mailbox_dir)
    mailbox.ensure()
    os.makedirs(config.state_dir, exist_ok=True)
    with exclusive_lock(os.path.join(config.state_dir, "request.lock")):
        if os.path.exists(mailbox.request_path):
            raise UpdaterError("an update request is already pending")
        rid = str(uuid.uuid4())
        _atomic_write_json(mailbox.request_path, {"request_id": rid, "target_version": "latest"})
    return rid


def read_status(config: Config) -> dict:
    """Read the sanitized outbox status (hub/CLI side). Absent = idle."""
    try:
        with open(Mailbox(config.mailbox_dir).status_path, "rb") as fh:
            data = json.loads(fh.read(MAX_MANIFEST + 1))
        if isinstance(data, dict):
            return data
    except (FileNotFoundError, ValueError, OSError):
        pass
    return {"state": IDLE, "request_id": "", "version": "", "message": "", "updated_at": ""}


def as_config_dict(config) -> dict:
    """Flatten a Config (or pass through a dict) to the single dict shape both
    adapters accept: top-level standard keys plus mode-specific extras."""
    if isinstance(config, dict):
        return config
    return {**config.extra, "mode": config.mode, "public_url": config.public_url,
            "ca_file": config.ca_file, "mailbox_dir": config.mailbox_dir,
            "state_dir": config.state_dir}


def build_adapter(config, transaction_dir: str):
    """Select the deployment adapter by mode, passing the flattened dict both
    adapters accept. Imported lazily so the engine stays importable without the
    adapter modules present."""
    merged = as_config_dict(config)
    mode = merged.get("mode")
    if mode == "native":
        from updater.native import NativeAdapter
        return NativeAdapter(merged, transaction_dir)
    if mode == "compose":
        from updater.compose import ComposeAdapter
        return ComposeAdapter(merged, transaction_dir)
    raise UpdaterError("unknown deployment mode")


def archive_completed_transaction(state_dir: str):
    """Rename a finished transaction dir to a unique `backup-<UTC>-<id>` so a
    new run starts on a clean journal while EVERY previous backup is retained
    (explicit cleanup is a documented operator step, never automatic here).
    Only called when no journal remains (i.e. the prior run finished)."""
    transaction_dir = os.path.join(state_dir, "transaction")
    if not os.path.isdir(transaction_dir):
        return None
    name = "backup-" + time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + "-" + uuid.uuid4().hex[:8]
    dest = os.path.join(state_dir, name)
    os.replace(transaction_dir, dest)
    return dest


def run_worker(config: Config, adapter=None) -> str:
    """systemd oneshot entry: under an exclusive lock, first recover any
    interrupted transaction; otherwise archive the previous completed
    transaction and apply a single pending request on a fresh journal."""
    os.makedirs(config.state_dir, exist_ok=True)
    transaction_dir = os.path.join(config.state_dir, "transaction")
    with exclusive_lock(os.path.join(config.state_dir, "worker.lock")):
        # An unfinished journal means a prior run was interrupted: resolve it on
        # its own transaction dir before touching anything new.
        if Journal(transaction_dir).exists():
            txn = Transaction(config, adapter or build_adapter(config, transaction_dir))
            return txn.recover()
        req = Mailbox(config.mailbox_dir).read_request()
        if req is None:
            return IDLE
        # Fresh request: retire the previous (finished) transaction dir so the
        # new adapter and journal start clean, then build the adapter.
        archive_completed_transaction(config.state_dir)
        txn = Transaction(config, adapter or build_adapter(config, transaction_dir))
        return txn.apply(req["request_id"], req["target_version"])


# ----- small helpers --------------------------------------------------------

def _utc_now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _is_uuid(value: str) -> bool:
    try:
        uuid.UUID(value)
        return True
    except (ValueError, AttributeError, TypeError):
        return False


def _short(message: str) -> str:
    message = "".join(c for c in str(message) if c.isprintable())
    return message[:200]


def _atomic_write_json(path: str, obj: dict, mode: int = 0o600):
    """Atomic JSON write with an explicit mode. Outbox files (status,
    capabilities, maintenance) are 0644 so the hub — running as root umask 077
    or as a different container user — can read them; private state stays 0600."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(obj, fh, sort_keys=True)
        fh.flush()
        os.fsync(fh.fileno())
    os.chmod(tmp, mode)
    os.replace(tmp, path)
    _fsync_parent(path)


def _fsync_parent(path: str):
    """fsync the directory holding path so a rename/create is durable, not just
    the file's data. A real fsync failure PROPAGATES (durability is promised
    before we accept traffic); only EINVAL — a filesystem that does not support
    directory fsync — is tolerated, since that is not a durability loss."""
    import errno
    parent = os.path.dirname(path) or "."
    try:
        fd = os.open(parent, os.O_RDONLY)
    except OSError as err:
        raise UpdaterError(f"cannot open {parent!r} to fsync: {type(err).__name__}")
    try:
        os.fsync(fd)
    except OSError as err:
        if err.errno == errno.EINVAL:
            return
        raise UpdaterError(f"durable directory fsync failed: {type(err).__name__}")
    finally:
        os.close(fd)


OUTBOX_MODE = 0o644
PRIVATE_MODE = 0o600
_READ_LIMIT = MAX_MANIFEST + 1


def _read_regular_bounded(path: str, limit: int) -> bytes:
    """Open with O_NOFOLLOW, confirm a regular file, and read within a bound.
    Used for anything a less-trusted writer can touch (the hub-group inbox)."""
    import stat
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode):
            raise UpdaterError("not a regular file")
        if info.st_size > limit:
            raise UpdaterError("file exceeds its size bound")
        return os.read(fd, limit + 1)
    finally:
        os.close(fd)


def _split(url: str):
    import urllib.parse
    return urllib.parse.urlsplit(url)


def _no_proxy_opener(context):
    return urllib.request.build_opener(urllib.request.HTTPSHandler(context=context),
                                       urllib.request.ProxyHandler({}))


def _remove_tree(path: str):
    with contextlib.suppress(FileNotFoundError):
        shutil.rmtree(path)


def safe_copy_regular(src: str, dst: str) -> tuple:
    """Copy a regular file as root without following a symlink at EITHER end
    (src may live in a user-writable dir; dst may be attacker-planted). Returns
    the source's (uid, gid, mode & 0o777) so a caller can restore ownership
    later. Raises UpdaterError if src is not a regular file."""
    import stat
    sfd = os.open(src, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(sfd)
        if not stat.S_ISREG(info.st_mode):
            raise UpdaterError("refusing to copy a non-regular file")
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        tmp = dst + ".tmp"
        with contextlib.suppress(FileNotFoundError):
            os.unlink(tmp)
        dfd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            while True:
                chunk = os.read(sfd, 1024 * 1024)
                if not chunk:
                    break
                os.write(dfd, chunk)
            os.fsync(dfd)
        finally:
            os.close(dfd)
        os.chmod(tmp, info.st_mode & 0o777)
        os.replace(tmp, dst)  # replaces a symlink at dst rather than following it
        _fsync_parent(dst)
        return (info.st_uid, info.st_gid, info.st_mode & 0o777)
    finally:
        os.close(sfd)
