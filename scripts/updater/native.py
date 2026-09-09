#!/usr/bin/env python3
"""Native (systemd) deployment adapter for the update engine.

Non-destructive by construction. The operator's install is NEVER deleted or
overwritten: prebuilt artifacts are placed in a root-owned, versioned release
directory, and the switch is made ONLY through root-owned systemd drop-ins
(dashboard ExecStart+WorkingDirectory → the release tree; hub ExecStart → the
release binary while its ORIGINAL WorkingDirectory, and therefore its SQLite
database, is left in place). The prior drop-in bytes are journaled and restored
exactly on rollback; the DB snapshot is restored with its ORIGINAL uid/gid so
the unprivileged hub still owns it.

Nothing is compiled on the host. The operator source checkout, untracked files,
agent binaries and signing material are never touched beyond being backed up.
All side effects are injected for tests.
"""
from __future__ import annotations

import os
import ipaddress
import platform
import re
import shutil
import time

from updater import engine

REQUIRED_FIELDS = ("hub_unit", "dashboard_unit", "proxy_unit", "hub_workdir",
                   "hub_home", "node_binary", "hub_url", "dashboard_url")
DB_FILES = ("bloxos.db", "bloxos.db-wal", "bloxos.db-shm")
IDENTITY_FILES = (".bloxos/jwt-secret", ".bloxos/setup-token")
DROPIN_NAME = "zz-bloxos-updater.conf"
DEFAULT_RELEASES_DIR = "/var/lib/bloxos-updater/releases"
DEFAULT_SYSTEMD_DIR = "/etc/systemd/system"
UNIT_RE = re.compile(r"[A-Za-z0-9@._-]+\.(service|target)\Z")
ELF_MACHINE = {"amd64": 0x3E, "arm64": 0xB7}


def default_runner(cmd):
    """Run a command, returning (rc, stdout, stderr). Real impl."""
    import subprocess
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
        return proc.returncode, proc.stdout, proc.stderr
    except (OSError, subprocess.SubprocessError) as err:
        return 1, "", f"{type(err).__name__}"


def current_arch() -> str:
    machine = platform.machine().lower()
    if machine in ("x86_64", "amd64"):
        return "amd64"
    if machine in ("aarch64", "arm64"):
        return "arm64"
    raise engine.UpdaterError(f"unsupported CPU architecture: {machine}")


class NativeAdapter:
    def __init__(self, config, transaction_dir, *, runner=default_runner,
                 fetch_bytes=None, identity_fetch=None, arch=None, discover=None,
                 ready_check=None, readiness_timeout=120.0, poll_interval=3.0,
                 sleep=None, monotonic=None):
        self.cfg = engine.as_config_dict(config)
        self.transaction_dir = transaction_dir
        self.backup_dir = os.path.join(transaction_dir, "backup")
        self.journal = engine.Journal(transaction_dir)
        self._runner = runner
        # Release downloads use the system trust store only (GitHub is public);
        # ca_file is only for the private-CA app endpoint, checked over the
        # public URL by the engine, not for artifact downloads.
        self._fetch = fetch_bytes or (lambda url, mx: engine.default_fetch_bytes(url, mx))
        self._identity = identity_fetch or (lambda base, comp: engine.default_identity_fetch(base, comp, None, 5.0))
        self._arch = arch
        # Fresh ownership evidence for preflight; injectable for tests.
        self._discover = discover or discover_native
        # Readiness probe: a restarted ORIGINAL must actually serve before we
        # claim a rollback/resume succeeded (systemctl start alone is not proof).
        self._ready = ready_check or _default_ready
        self.readiness_timeout = readiness_timeout
        self.poll_interval = poll_interval
        self._sleep = sleep or time.sleep
        self._monotonic = monotonic or time.monotonic
        self.systemd_dir = self.cfg.get("systemd_dir", DEFAULT_SYSTEMD_DIR)
        self.releases_dir = self.cfg.get("releases_dir", DEFAULT_RELEASES_DIR)

    # --- helpers -----------------------------------------------------------
    def _f(self, name):
        value = self.cfg.get(name)
        if not isinstance(value, str) or not value:
            raise engine.UpdaterError(f"native config missing field: {name}")
        return value

    def _unit(self, name):
        unit = self._f(name)
        if not UNIT_RE.fullmatch(unit):
            raise engine.UpdaterError(f"native config has an implausible unit name for {name}")
        return unit

    def _systemctl(self, verb, *units):
        rc, _out, err = self._runner(["systemctl", verb, *units])
        if rc != 0:
            raise engine.UpdaterError(f"systemctl {verb} failed" + (f" for {units[0]}" if units else ""))

    def _dropin_path(self, unit):
        return os.path.join(self.systemd_dir, unit + ".d", DROPIN_NAME)

    def _release_dir(self, manifest):
        return os.path.join(self.releases_dir, f"{manifest['version']}-{manifest['revision']}")

    # --- adapter contract --------------------------------------------------
    def preflight(self):
        """Re-confirm this host's ownership against FRESH evidence before every
        update — not just that recorded paths exist. Re-runs discovery and
        compares the STABLE identity (units, hub DB working directory, hub home,
        public URL) and re-proves the public edge still routes to this host.

        Drop-in tolerant: a prior successful update makes the running hub the
        release binary and the dashboard `node server.js`, so hub_binary /
        node_binary / dashboard_workdir legitimately differ from the recorded
        config and are deliberately NOT compared. The DB working directory and
        the units must not move; the edge must still be ours."""
        for field in REQUIRED_FIELDS:
            self._f(field)
        for unit_field in ("hub_unit", "dashboard_unit", "proxy_unit"):
            self._unit(unit_field)
        if not os.path.isdir(self._f("hub_workdir")):
            raise engine.UpdaterError("recorded hub working directory is missing; re-run init")
        if not os.path.isdir(self._f("hub_home")):
            raise engine.UpdaterError("recorded hub home is missing; re-run init")

        disc = self._discover()
        if disc.get("ambiguous"):
            raise engine.UpdaterError("cannot re-confirm this host's deployment: " + disc["ambiguous"])
        for key in ("hub_unit", "dashboard_unit", "proxy_unit", "hub_workdir", "hub_home", "public_url"):
            if disc.get(key) != self.cfg.get(key):
                raise engine.UpdaterError(
                    f"the running deployment no longer matches the recorded {key}; re-run init")
        if not disc.get("routing_verified"):
            raise engine.UpdaterError(
                "cannot prove the public edge still routes to this host's hub: "
                + (disc.get("routing_reason") or "unverified"))

    def stage(self, manifest, release_dir):
        """Download, checksum-verify and safely extract the prebuilt bundle,
        verify its ELF architecture and dashboard server BEFORE any downtime,
        then place it in the root-owned versioned release directory."""
        arch = self._arch or current_arch()
        native = manifest.get("native", {})
        if arch not in native:
            raise engine.UpdaterError(f"release has no native bundle for {arch}")
        entry = native[arch]
        url = engine.RELEASE_ASSET.format(version=manifest["version"], file=entry["file"])
        os.makedirs(release_dir, exist_ok=True)
        archive = os.path.join(release_dir, entry["file"])
        engine.download_and_verify(url, archive, entry["sha256"], self._fetch)
        extract_dir = os.path.join(release_dir, "bundle")
        engine._remove_tree(extract_dir)
        engine.safe_extract_tar(archive, extract_dir)

        staged_hub = os.path.join(extract_dir, "hub", "bloxos-hub")
        staged_server = os.path.join(extract_dir, "dashboard", "server.js")
        staged_next = os.path.join(extract_dir, "dashboard", ".next")
        if not os.path.isfile(staged_hub):
            raise engine.UpdaterError("bundle missing hub/bloxos-hub")
        _verify_elf_arch(staged_hub, arch)
        if not os.path.isfile(staged_server):
            raise engine.UpdaterError("bundle missing dashboard/server.js (not a standalone build)")
        if not os.path.isdir(staged_next):
            raise engine.UpdaterError("bundle missing dashboard/.next")

        # A repeated release may already be serving from this version/revision.
        # Stage under a unique name; NEVER delete the running release tree.
        target = self._release_dir(manifest) + "-" + engine.uuid.uuid4().hex[:12]
        parent = os.path.dirname(target)
        os.makedirs(parent, exist_ok=True)
        if os.path.islink(parent) or os.stat(parent).st_uid != os.geteuid() or os.stat(parent).st_mode & 0o022:
            raise engine.UpdaterError("release directory is not privately controlled by the updater")
        os.chmod(parent, 0o755)
        shutil.copytree(extract_dir, target, symlinks=True)
        os.chmod(self._f_release_hub(target), 0o755)
        for base, _dirs, files in os.walk(target, followlinks=False):
            for name in files:
                path = os.path.join(base, name)
                if not os.path.islink(path):
                    with open(path, "rb") as stream:
                        os.fsync(stream.fileno())
            _fsync_dir(base)
        _fsync_dir(parent)
        self.journal.record("release_dir", target)

    def _f_release_hub(self, release_dir):
        return os.path.join(release_dir, "hub", "bloxos-hub")

    def quiesce(self):
        """Stop the PUBLIC proxy first, then hub and dashboard. Failures raise
        so the engine never proceeds to a backup behind a live proxy."""
        self.journal.record("quiesced", True)
        self._systemctl("stop", self._unit("proxy_unit"))
        self._systemctl("stop", self._unit("hub_unit"))
        self._systemctl("stop", self._unit("dashboard_unit"))

    def backup(self):
        """With services stopped, snapshot the DB (+WAL/SHM) and identity files
        WITHOUT following symlinks, recording their original uid/gid/mode; and
        snapshot the CURRENT drop-in files (or their absence) for exact restore.
        The operator's dashboard/hub directories are never copied wholesale."""
        os.makedirs(self.backup_dir, exist_ok=True)
        hub_workdir = self._f("hub_workdir")
        db_meta = {}
        for name in DB_FILES:
            src = os.path.join(hub_workdir, name)
            if os.path.lexists(src):
                uid, gid, mode = engine.safe_copy_regular(src, os.path.join(self.backup_dir, "db", name))
                db_meta[name] = [uid, gid, mode]
        self.journal.record("db_meta", db_meta)

        home = self._f("hub_home")
        id_meta = {}
        for rel in IDENTITY_FILES:
            src = os.path.join(home, rel)
            if os.path.lexists(src):
                uid, gid, mode = engine.safe_copy_regular(src, os.path.join(self.backup_dir, "identity", rel))
                id_meta[rel] = [uid, gid, mode]
        self.journal.record("id_meta", id_meta)

        dropins = {}
        for unit_field in ("hub_unit", "dashboard_unit"):
            unit = self._unit(unit_field)
            path = self._dropin_path(unit)
            saved = os.path.join(self.backup_dir, "dropins", unit + ".conf")
            if os.path.lexists(path):
                uid, gid, mode = engine.safe_copy_regular(path, saved)
                dropins[unit] = {"existed": True, "mode": mode}
            else:
                dropins[unit] = {"existed": False}
        self.journal.record("dropins", dropins)
        # safe_copy_regular fsync'd each file; fsync the backup dir too so the
        # snapshot is durable BEFORE the committing marker is recorded.
        _fsync_dir(self.backup_dir)
        self.journal.record("backed_up", True)

    def install(self):
        """Switch via root-owned drop-ins only. Hub keeps its original
        WorkingDirectory (DB stays put); dashboard runs the release tree."""
        release = self.journal.get("release_dir")
        if not release:
            raise engine.UpdaterError("no staged release recorded")
        hub_exec = os.path.join(release, "hub", "bloxos-hub")
        dash_server = os.path.join(release, "dashboard", "server.js")
        node = self._f("node_binary")
        endpoint = engine._split(self._f("dashboard_url"))
        try:
            bind = ipaddress.ip_address(endpoint.hostname)
            port = endpoint.port or 80
        except ValueError:
            raise engine.UpdaterError("dashboard endpoint must be a loopback IP and valid port")
        if endpoint.scheme != "http" or not bind.is_loopback:
            raise engine.UpdaterError("dashboard endpoint must be loopback HTTP")
        self._write_dropin(self._unit("hub_unit"),
                           f"[Service]\nExecStart=\nExecStart={hub_exec}\n")
        self._write_dropin(self._unit("dashboard_unit"),
                           f"[Service]\nExecStart=\nExecStart={node} {dash_server}\n"
                           f"WorkingDirectory={release}/dashboard\n"
                           f"Environment=HOSTNAME={bind} PORT={port}\n")
        self._systemctl("daemon-reload")
        self.journal.record("installed", True)

    def _write_dropin(self, unit, content):
        path = self._dropin_path(unit)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(content)
            fh.flush()
            os.fsync(fh.fileno())
        os.chmod(tmp, 0o644)
        os.replace(tmp, path)
        engine._fsync_parent(path)
        engine._fsync_parent(os.path.dirname(path))

    def start_candidate(self):
        self._systemctl("start", self._unit("hub_unit"))
        self._systemctl("start", self._unit("dashboard_unit"))
        self._systemctl("start", self._unit("proxy_unit"))

    def identities(self):
        return {
            "hub": self._identity(self._f("hub_url"), "hub"),
            "dashboard": self._identity(self._f("dashboard_url"), "dashboard"),
        }

    def rollback(self):
        """Restore the original drop-ins EXACTLY and the DB snapshot with its
        original ownership, then restart. Stop failures BEFORE the DB restore
        propagate (never restore under a live hub); start failures propagate
        (never claim a rollback that didn't restart). No suppression."""
        self._systemctl("stop", self._unit("proxy_unit"))
        self._systemctl("stop", self._unit("hub_unit"))
        self._systemctl("stop", self._unit("dashboard_unit"))

        if not self.journal.get("backed_up"):
            # Nothing was installed and no snapshot exists: just restart what we
            # stopped. (resume_original covers the pre-backup case.)
            self._start_backends()
            return

        # Drop-ins: restore prior bytes exactly, or remove the one we added.
        dropins = self.journal.get("dropins", {})
        for unit_field in ("hub_unit", "dashboard_unit"):
            unit = self._unit(unit_field)
            path = self._dropin_path(unit)
            info = dropins.get(unit, {"existed": False})
            if info.get("existed"):
                saved = os.path.join(self.backup_dir, "dropins", unit + ".conf")
                engine.safe_copy_regular(saved, path)
                os.chmod(path, info.get("mode", 0o644))
            else:
                _remove_file(path)
                _rmdir_if_empty(os.path.dirname(path))
        self._systemctl("daemon-reload")

        # DB: restore bytes AND original uid/gid/mode; drop any post-backup WAL.
        hub_workdir = self._f("hub_workdir")
        db_meta = self.journal.get("db_meta", {})
        for name in DB_FILES:
            dst = os.path.join(hub_workdir, name)
            saved = os.path.join(self.backup_dir, "db", name)
            if name in db_meta and os.path.lexists(saved):
                engine.safe_copy_regular(saved, dst)
                uid, gid, mode = db_meta[name]
                os.chown(dst, uid, gid)
                os.chmod(dst, mode)
            else:
                _remove_file(dst)  # created after the snapshot; discard

        id_meta = self.journal.get("id_meta", {})
        home = self._f("hub_home")
        for rel in IDENTITY_FILES:
            if rel in id_meta:
                saved = os.path.join(self.backup_dir, "identity", rel)
                dst = os.path.join(home, rel)
                engine.safe_copy_regular(saved, dst)
                uid, gid, mode = id_meta[rel]
                os.chown(dst, uid, gid)
                os.chmod(dst, mode)

        self._start_backends()

    def resume_original(self):
        """Restart the original components WITHOUT restoring anything — used
        only when backup failed before any install. Failures propagate."""
        self._start_backends()

    def _start_backends(self):
        """Start ONLY the loopback backends (hub + dashboard) and wait for them
        to answer — NOT the public proxy. rollback()/resume_original() use this
        so a rolled-back/legacy hub never accepts PUBLIC writes before the
        transaction reaches a durable terminal (ROLLED_BACK/FAILED) and the
        engine calls finalize(). The backends are loopback-only until the proxy
        reopens, and the maintenance marker still gates writes, so bringing them
        up here is safe."""
        self._systemctl("start", self._unit("hub_unit"))
        self._systemctl("start", self._unit("dashboard_unit"))
        # Do not report a completed rollback/resume until the restarted original
        # actually answers (a legacy hub has no build-info, so this is a
        # liveness probe: hub /health and the dashboard root, bounded).
        self._wait_ready()

    def finalize(self):
        """Reopen the PUBLIC proxy after a durable terminal state. The engine
        calls this from _finish() AFTER SUCCEEDED/ROLLED_BACK/FAILED(resume) is
        journalled and BEFORE clearing the maintenance marker, and again on
        terminal recovery. Idempotent: `systemctl start` on an already-active
        proxy is a no-op, so a second recovery pass (or a crash-and-retry)
        re-issues it harmlessly. This is the ONLY place the proxy is started on
        the rollback/resume terminals.

        Boot: the recovery gate injects a runner that turns this one start into
        `systemctl start --no-block <proxy>` (the proxy is ordered After= the
        gate, so a blocking start inside the gate's own ExecStart would
        self-wait). The enqueue happens only here — after a durable terminal —
        so restarting the gate after a fixed failure re-opens Caddy; forward
        `Requires=` never restarts a cancelled proxy job on its own.

        No HTTP readiness probe: on the success path the engine already verified
        public identities through the proxy before finalize; on rollback the
        original Caddy config is restored and its active state is the proof; and
        at boot the proxy starts only AFTER this gate exits, so a synchronous
        probe here could not observe it. `systemctl start` blocking until the
        unit is active is the runtime readiness."""
        self._systemctl("start", self._unit("proxy_unit"))

    def _wait_ready(self):
        targets = [self._f("hub_url").rstrip("/") + "/health",
                   self._f("dashboard_url").rstrip("/") + "/"]
        deadline = self._monotonic() + self.readiness_timeout
        while True:
            try:
                for url in targets:
                    self._ready(url)
                return
            except (engine.UpdaterError, OSError, RuntimeError):
                if self._monotonic() >= deadline:
                    raise engine.UpdaterError(
                        "restarted original services did not become ready; recovery state retained")
                self._sleep(self.poll_interval)


# ----- artifact/arch validation --------------------------------------------

def _verify_elf_arch(path, arch):
    with open(path, "rb") as fh:
        head = fh.read(20)
    if head[:4] != b"\x7fELF":
        raise engine.UpdaterError("staged hub binary is not an ELF executable")
    if head[4] != 2 or head[5] != 1:
        raise engine.UpdaterError("staged hub binary is not 64-bit little-endian")
    machine = head[18] | (head[19] << 8)
    if machine != ELF_MACHINE[arch]:
        raise engine.UpdaterError(f"staged hub binary is the wrong architecture for {arch}")


def _default_ready(url):
    """Liveness probe for a restarted original: reachable and not 5xx."""
    import urllib.error
    import urllib.request
    try:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(url, timeout=5) as resp:
            if getattr(resp, "status", 200) >= 500:
                raise engine.UpdaterError("service returned a server error")
    except urllib.error.HTTPError as err:
        if err.code >= 500:
            raise engine.UpdaterError("service returned a server error")
    except OSError as err:
        raise engine.UpdaterError("service unreachable: %s" % type(err).__name__)


def _remove_file(path):
    import contextlib
    with contextlib.suppress(FileNotFoundError):
        os.remove(path)
        engine._fsync_parent(path)


def _rmdir_if_empty(path):
    import contextlib
    with contextlib.suppress(OSError):
        os.rmdir(path)


# ----- durability + one-time setup evidence --------------------------------

def _fsync_dir(path):
    """Propagate a real dir fsync failure (durability); tolerate only EINVAL."""
    import errno
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError as err:
        raise engine.UpdaterError("cannot open backup dir to fsync: %s" % type(err).__name__)
    try:
        os.fsync(fd)
    except OSError as err:
        if err.errno == errno.EINVAL:
            return
        raise engine.UpdaterError("durable backup fsync failed: %s" % type(err).__name__)
    finally:
        os.close(fd)


def discover_native(repo_dir=None, *, show=None, read_proc=None, listeners=None,
                    caddy_config=None, leaf_cert=None, read_cgroup=None,
                    proxy_candidates=("caddy.service", "bloxos-caddy.service")) -> dict:
    """Read-only evidence for a one-time `init`, grounded in the ACTUAL running
    services and the ACTIVE proxy config — never unit-file text, /home guesses,
    or hardcoded ports:

      - `systemctl show` gives MainPID/ActiveState;
      - /proc/<pid>/exe is the real executable (argv is rewritten by pnpm/next),
        /proc/<pid>/environ gives HOME, PUBLIC_URL and BLOXOS_CA_CERT, cwd is
        the working directory (dashboard_workdir captured as EVIDENCE only);
      - the hub and dashboard loopback listeners are identified by who actually
        listens (ss/proc), not by an assumed port;
      - routing is PROVEN, not assumed: the active Caddy admin config must route
        to those owned loopback backends AND the certificate served on the local
        :443 must equal the one served on the public URL. An active Caddy alone,
        or an operator's say-so, is never accepted as proof.

    Legacy v1.2.2 (no build-info) is supported because the proof is transport
    and config based, not build-info based. A custom/ambiguous layout returns
    `ambiguous`; nothing is mutated."""
    show = show or _systemctl_show
    read_proc = read_proc or _read_proc
    listeners = listeners or _listeners
    caddy_config = caddy_config or _caddy_config
    leaf_cert = leaf_cert or _leaf_cert
    read_cgroup = read_cgroup or _read_cgroup

    hub = show("bloxos-hub.service")
    dash = show("bloxos-dashboard.service")
    if hub.get("ActiveState") != "active" or dash.get("ActiveState") != "active":
        return {"ambiguous": "bloxos-hub/bloxos-dashboard are not both active"}
    proxy_unit = next((c for c in proxy_candidates if show(c).get("ActiveState") == "active"), None)
    if not proxy_unit:
        return {"ambiguous": "no active Caddy/proxy unit found"}
    if not _is_pid(hub.get("MainPID")):
        return {"ambiguous": "hub has no running MainPID"}

    # Identify listeners by cgroup MEMBERSHIP of the selected unit — never by
    # "any node listener" or an exe-name guess, which could belong to a foreign
    # process. A unit's cgroup contains all its descendants (so a pnpm->node
    # child of the dashboard service still matches bloxos-dashboard.service).
    # Ownership is the unit's ACTUAL ControlGroup (from systemctl show) vs the
    # pid's parsed /proc cgroup path — exact or a proper child prefix. Never a
    # basename substring (which fake-bloxos-hub.service or user.service would
    # spoof), and never inferred from the unit name.
    proxy = show(proxy_unit)
    hub_cg = hub.get("ControlGroup", "")
    dash_cg = dash.get("ControlGroup", "")
    proxy_cg = proxy.get("ControlGroup", "")
    if not (hub_cg and dash_cg and proxy_cg):
        return {"ambiguous": "cannot read unit control groups from systemctl"}

    lis = listeners()

    def owned(control_group, predicate):
        return [l for l in lis if predicate(l) and _in_cgroup(read_cgroup, l.get("pid"), control_group)]

    hub_listeners = owned(hub_cg, lambda l: _is_loopback(l.get("addr", "")))
    if len(hub_listeners) != 1:
        return {"ambiguous": "no unique loopback listener in the bloxos-hub.service cgroup"}
    hub_listener = hub_listeners[0]
    dash_listeners = owned(dash_cg, lambda l: _is_loopback(l.get("addr", "")))
    if len(dash_listeners) != 1:
        return {"ambiguous": "no unique loopback listener in the bloxos-dashboard.service cgroup"}
    dash_listener = dash_listeners[0]
    # The public edge AND the Caddy admin API must both live in the selected
    # proxy unit's cgroup — an active Caddy elsewhere is not our edge.
    if not owned(proxy_cg, lambda l: l.get("port") == 443):
        return {"ambiguous": "the :443 listener is not in the selected proxy unit's cgroup"}
    if not owned(proxy_cg, lambda l: l.get("port") == 2019):
        return {"ambiguous": "the Caddy admin (:2019) is not in the selected proxy unit's cgroup"}

    hub_proc = read_proc(hub_listener["pid"])
    hub_binary = hub_proc.get("exe", "") or hub_listener.get("exe", "")
    env = hub_proc.get("environ", {}) or {}
    hub_home = env.get("HOME", "")
    public_url = (env.get("PUBLIC_URL") or "").strip()
    hub_workdir = hub_proc.get("cwd", "") or hub_listener.get("cwd", "")
    ca_file = (env.get("BLOXOS_CA_CERT") or "").strip() or None
    dash_proc = read_proc(dash_listener["pid"])
    node_binary = dash_proc.get("exe", "") or dash_listener.get("exe", "")
    dash_workdir = dash_proc.get("cwd", "") or dash_listener.get("cwd", "")
    if not (hub_binary and os.path.isabs(hub_binary)):
        return {"ambiguous": "hub executable is not an absolute path"}
    if not (node_binary and os.path.isabs(node_binary)):
        return {"ambiguous": "dashboard executable is not an absolute path"}
    if not public_url:
        return {"ambiguous": "hub process has no PUBLIC_URL; cannot verify the public edge"}

    def listener_url(listener):
        address = listener["addr"].strip("[]")
        host = f"[{address}]" if ":" in address else address
        return f"http://{host}:{listener['port']}"

    hub_url = listener_url(hub_listener)
    dash_url = listener_url(dash_listener)
    routing_verified, reason = _verify_routing(public_url, hub_url, dash_url, ca_file,
                                               caddy_config, leaf_cert)

    return {
        "mode": "native",
        "hub_unit": "bloxos-hub.service", "dashboard_unit": "bloxos-dashboard.service",
        "proxy_unit": proxy_unit,
        "hub_binary": hub_binary, "hub_workdir": hub_workdir, "hub_home": hub_home,
        "node_binary": node_binary, "dashboard_workdir": dash_workdir,
        "hub_url": hub_url, "dashboard_url": dash_url,
        "public_url": public_url, "ca_file": ca_file,
        "routing_verified": routing_verified, "routing_reason": reason,
        "evidence": {"hub_pid": int(hub["MainPID"]), "dashboard_pid": dash_listener.get("pid"),
                     "proxy_unit": proxy_unit},
    }


def _verify_routing(public_url, hub_url, dash_url, ca_file, caddy_config, leaf_cert):
    """Proof, not confirmation: (1) the active Caddy config routes to the owned
    loopback hub/dashboard; (2) the local :443 leaf equals the public :443 leaf,
    verified under BLOXOS_CA_CERT (private) or system trust (public)."""
    try:
        cfg = caddy_config()
    except engine.UpdaterError as err:
        return False, "active proxy config unavailable: %s" % err
    upstreams = {_norm_hostport(u) for u in _caddy_upstreams(cfg)}
    if _norm_hostport(_hostport(hub_url)) not in upstreams or \
       _norm_hostport(_hostport(dash_url)) not in upstreams:
        return False, "active proxy config does not route to the owned loopback listeners"
    pub = engine._split(public_url)
    if pub.scheme != "https":
        return False, "public URL is not https; cannot prove the edge by certificate"
    host = pub.hostname
    port = pub.port or 443
    try:
        local = leaf_cert("127.0.0.1", 443, host, ca_file)
        remote = leaf_cert(host, port, host, ca_file)
    except engine.UpdaterError as err:
        return False, "could not compare local and public certificates: %s" % err
    if not local or local != remote:
        return False, "the local :443 certificate does not match the public endpoint"
    return True, "proxy routes owned loopback backends and the local edge serves the public certificate"


# ----- real (best-effort) evidence seams; tests inject fakes ---------------

def _systemctl_show(unit):
    rc, out, _err = default_runner(
        ["systemctl", "show", unit, "-p", "MainPID", "-p", "ActiveState",
         "-p", "LoadState", "-p", "ControlGroup"])
    props = {}
    if rc == 0:
        for line in out.splitlines():
            key, _, value = line.partition("=")
            props[key.strip()] = value.strip()
    return props


def _read_proc(pid):
    base = "/proc/%d" % pid
    out = {"exe": "", "environ": {}, "cwd": ""}
    import contextlib
    with contextlib.suppress(OSError):
        out["exe"] = os.readlink(os.path.join(base, "exe"))
    with contextlib.suppress(OSError):
        with open(os.path.join(base, "environ"), "rb") as fh:
            for item in fh.read().split(b"\x00"):
                if b"=" in item:
                    k, _, v = item.partition(b"=")
                    out["environ"][k.decode("utf-8", "replace")] = v.decode("utf-8", "replace")
    with contextlib.suppress(OSError):
        out["cwd"] = os.readlink(os.path.join(base, "cwd"))
    return out


def _listeners():
    """Loopback TCP listeners as [{port, addr, pid, exe, cwd}] via `ss`."""
    rc, out, _err = default_runner(["ss", "-Hltnp"])
    result = []
    if rc != 0:
        return result
    for line in out.splitlines():
        cols = line.split()
        if len(cols) < 4:
            continue
        laddr = cols[3]
        host, _, port = laddr.rpartition(":")
        if not port.isdigit():
            continue
        pid = None
        m = re.search(r"pid=(\d+)", line)
        if m:
            pid = int(m.group(1))
        entry = {"port": int(port), "addr": host, "pid": pid, "exe": "", "cwd": ""}
        if pid:
            proc = _read_proc(pid)
            entry["exe"] = proc.get("exe", "")
            entry["cwd"] = proc.get("cwd", "")
        result.append(entry)
    return result


def _caddy_config(admin="http://127.0.0.1:2019/config/"):
    import urllib.request
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(admin, timeout=5) as resp:
            body = resp.read(4 * 1024 * 1024 + 1)
    except OSError as err:
        raise engine.UpdaterError("caddy admin unreachable: %s" % type(err).__name__)
    import json as _json
    try:
        return _json.loads(body)
    except ValueError:
        raise engine.UpdaterError("caddy admin config is not JSON")


def _leaf_cert(host, port, server_name, ca_file):
    import socket
    import ssl
    # Augment system trust with the private CA when present (do not replace it),
    # so a public-CA edge with a leftover private CA still verifies.
    context = ssl.create_default_context()
    if ca_file:
        context.load_verify_locations(cafile=ca_file)
    try:
        with socket.create_connection((host, int(port)), timeout=5) as sock:
            with context.wrap_socket(sock, server_hostname=server_name) as tls:
                return tls.getpeercert(binary_form=True)
    except (OSError, ssl.SSLError) as err:
        raise engine.UpdaterError("TLS probe of %s failed: %s" % (server_name, type(err).__name__))


# ----- small pure helpers ---------------------------------------------------

def _caddy_upstreams(node, acc=None):
    acc = acc if acc is not None else set()
    if isinstance(node, dict):
        ups = node.get("upstreams")
        if isinstance(ups, list):
            for u in ups:
                if isinstance(u, dict) and isinstance(u.get("dial"), str):
                    acc.add(u["dial"])
        for value in node.values():
            _caddy_upstreams(value, acc)
    elif isinstance(node, list):
        for item in node:
            _caddy_upstreams(item, acc)
    return acc


def _hostport(url):
    parts = engine._split(url)
    return "%s:%d" % (parts.hostname, parts.port or 80)


def _norm_hostport(hostport):
    host, _, port = hostport.rpartition(":")
    if host in ("localhost", "::1", ""):
        host = "127.0.0.1"
    return "%s:%s" % (host, port)


def _is_loopback(addr):
    try:
        return ipaddress.ip_address(addr.strip("[]")).is_loopback
    except ValueError:
        return False


def _read_cgroup(pid):
    """Return /proc/<pid>/cgroup text (empty on error). The line names the
    owning systemd unit, e.g. '0::/system.slice/bloxos-dashboard.service'."""
    try:
        with open("/proc/%d/cgroup" % int(pid), "r", encoding="utf-8", errors="replace") as fh:
            return fh.read()
    except (OSError, TypeError, ValueError):
        return ""


def _cgroup_path(text):
    """Parse a /proc/<pid>/cgroup blob to the unified (v2) path. A v2 line is
    '0::/system.slice/foo.service'; fall back to the last field of the first
    line for hybrid hierarchies."""
    lines = (text or "").splitlines()
    for line in lines:
        parts = line.split(":", 2)
        if len(parts) == 3 and parts[1] == "":
            return parts[2].strip()
    if lines:
        return lines[0].split(":", 2)[-1].strip()
    return ""


def _in_cgroup(read_cgroup, pid, control_group):
    """A PID belongs to a unit iff its parsed cgroup path EQUALS the unit's
    actual ControlGroup (from systemctl show) or is a proper child of it. This
    matches a pnpm->node child of the dashboard service while rejecting a
    same-basename impostor (fake-bloxos-hub.service) or an unrelated slice."""
    if not pid or not control_group:
        return False
    path = _cgroup_path(read_cgroup(pid))
    if not path:
        return False
    cg = control_group.rstrip("/")
    return path == cg or path.startswith(cg + "/")


def _is_pid(value):
    try:
        return int(value) > 0
    except (TypeError, ValueError):
        return False
