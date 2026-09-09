#!/usr/bin/env python3
"""DISPOSABLE-VM-ONLY native updater smoke fixture. DO NOT run on any real or
shared host — it creates real systemd units and a real Caddy.

It uses a DEDICATED fresh root (default /tmp/bloxos-updater-native-smoke) and
its own Caddy storage; it never touches /tmp/bloxos-updater-test or
/etc/bloxos-updater (the existing Compose fixture/config), and refuses to
overwrite any pre-existing bloxos-hub/dashboard/caddy unit or Caddyfile.

It stands up a standard native deployment (hub + dashboard + Caddy) from a
locally exported *original* server bundle (which may be a legacy v1.2.2 with no
build-info), seeds a real sqlite3 fixture row owned by an unprivileged user plus
an untracked file and an operator source dir, then drives the real update engine
+ native adapter (real systemctl / TLS; only the GitHub release fetch is
redirected to a local *candidate* bundle) through:

  1. success  — update to the candidate; public+direct build-info match the
                candidate manifest; sqlite row, untracked file, operator source
                and DB ownership all intact.
  2. rollback — a manifest whose revision != the candidate binaries fails
                verification and rolls back to the PRE-transaction state (the
                candidate from step 1, NOT the original); data + ownership
                intact.

Readiness of the (possibly build-info-less) original is a /health liveness
probe; build-info is required only of the upgraded candidate.

Guards (ALL required): SMOKE_CONFIRM_DISPOSABLE=1, root, Linux+systemd, ports
443/4000/3000/2019 free (stop any Compose stack first), fresh state root, and no
pre-existing target units/Caddyfile.

Usage (disposable Lima VM, AFTER stopping the Compose stack):
  sudo SMOKE_CONFIRM_DISPOSABLE=1 python3 scripts/smoke/updater-native.py \
      --original-bundle /tmp/pair/original --candidate-bundle /tmp/pair/candidate
Each --*-bundle dir is an export-server-bundle.py --output directory.
"""
import argparse
import contextlib
import json
import os
import pwd
import shutil
import socket
import subprocess
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater import engine, native  # noqa: E402

TEST_ROOT = Path("/tmp/bloxos-updater-native-smoke")
CADDYFILE = Path("/etc/caddy/Caddyfile")
TEST_HOST = "hub.updater-smoke.test"
UNITS = ("bloxos-hub.service", "bloxos-dashboard.service", "caddy.service")
HOSTS_LINE = f"127.0.0.1 {TEST_HOST}\n"
CREATED = {"caddy_dir": False, "hosts_line": False}


def die(message):
    raise SystemExit("smoke: " + message)


def caddy_root():
    return TEST_ROOT / "caddy-data/pki/authorities/local/root.crt"


def require_disposable(args):
    if os.environ.get("SMOKE_CONFIRM_DISPOSABLE") != "1":
        die("refusing to run without SMOKE_CONFIRM_DISPOSABLE=1 (disposable VM only)")
    if os.geteuid() != 0:
        die("must run as root on a disposable VM")
    if sys.platform != "linux" or not shutil.which("systemctl"):
        die("requires Linux with systemd")
    if TEST_ROOT.exists():
        die(f"{TEST_ROOT} already exists — use a fresh state root (--state-root) or remove it yourself")
    for unit in UNITS:
        if Path("/etc/systemd/system", unit).exists():
            die(f"/etc/systemd/system/{unit} already exists — refusing to overwrite it")
        if subprocess.run(["systemctl", "is-active", unit], capture_output=True, text=True).stdout.strip() == "active":
            die(f"{unit} is already active — stop any real/Compose deployment first")
    if CADDYFILE.exists():
        die(f"{CADDYFILE} already exists — refusing to overwrite it")
    for port in (443, 4000, 3000, 2019):
        with contextlib.closing(socket.socket()) as probe:
            if probe.connect_ex(("127.0.0.1", port)) == 0:
                die(f"port {port} is in use — stop the Compose stack before running")
    for tool in ("caddy", "node", "sqlite3"):
        if not shutil.which(tool):
            die(f"{tool} not found")
    for label in ("original_bundle", "candidate_bundle"):
        if not (Path(getattr(args, label)) / "update-manifest.json").is_file():
            die(f"{label} is not an export-server-bundle output directory")


def sh(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True)


def extract_bundle(bundle_dir, dest):
    manifest = json.loads((Path(bundle_dir) / "update-manifest.json").read_text())
    entry = manifest["native"][native.current_arch()]
    engine.safe_extract_tar(str(Path(bundle_dir) / entry["file"]), str(dest))
    return manifest


def write(path, text, mode=0o644):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    os.chmod(path, mode)


def unprivileged_user():
    for name in ("bloxi", "ubuntu", "nobody"):
        with contextlib.suppress(KeyError):
            record = pwd.getpwnam(name)
            if record.pw_uid != 0:
                return record
    die("no unprivileged user available to own the DB")


def db_path():
    return TEST_ROOT / "hub-workdir/bloxos.db"


def seed_db_as_user(user):
    """Create a REAL sqlite fixture table/row, owned by the unprivileged user,
    before the hub starts. No text is ever written into the DB file directly."""
    code = ("import sqlite3,sys;"
            "c=sqlite3.connect(sys.argv[1]);"
            "c.execute('CREATE TABLE IF NOT EXISTS smoke_fixture(k TEXT PRIMARY KEY, v TEXT)');"
            "c.execute(\"INSERT OR REPLACE INTO smoke_fixture VALUES('seed','SEEDED')\");"
            "c.commit();c.close()")

    def drop():
        os.setgid(user.pw_gid)
        os.setuid(user.pw_uid)
    subprocess.run([sys.executable, "-c", code, str(db_path())], check=True, preexec_fn=drop)


def read_fixture_row():
    """Read-only (no -wal/-shm creation, no ownership side effects)."""
    import sqlite3
    con = sqlite3.connect(f"file:{db_path()}?mode=ro", uri=True)
    try:
        row = con.execute("SELECT v FROM smoke_fixture WHERE k='seed'").fetchone()
        return row[0] if row else None
    finally:
        con.close()


def db_owner():
    st = os.stat(db_path())
    return st.st_uid, st.st_gid


def install_original(release_dir, user):
    hub_workdir = TEST_ROOT / "hub-workdir"
    hub_home = TEST_ROOT / "home"
    (hub_home / ".bloxos").mkdir(parents=True, exist_ok=True)
    hub_workdir.mkdir(parents=True, exist_ok=True)
    for path in (hub_workdir, hub_home, hub_home / ".bloxos"):
        os.chown(path, user.pw_uid, user.pw_gid)
    write("/etc/systemd/system/bloxos-hub.service", f"""[Unit]
After=network.target
[Service]
User={user.pw_name}
WorkingDirectory={hub_workdir}
ExecStart={release_dir}/hub/bloxos-hub
Environment=HOME={hub_home}
Environment=PUBLIC_URL=https://{TEST_HOST}
Environment=HUB_LISTEN=127.0.0.1:4000
Environment=BLOXOS_CA_CERT={caddy_root()}
Environment=BLOXOS_UPDATER_DIR={TEST_ROOT}/mailbox
Restart=always
[Install]
WantedBy=multi-user.target
""")
    write("/etc/systemd/system/bloxos-dashboard.service", f"""[Unit]
After=network.target bloxos-hub.service
[Service]
User={user.pw_name}
WorkingDirectory={release_dir}/dashboard
ExecStart={shutil.which("node")} {release_dir}/dashboard/server.js
Environment=NODE_ENV=production
Environment=HOSTNAME=127.0.0.1
Environment=PORT=3000
Restart=always
[Install]
WantedBy=multi-user.target
""")
    if not CADDYFILE.parent.exists():
        CADDYFILE.parent.mkdir(parents=True)
        CREATED["caddy_dir"] = True
    write(CADDYFILE, f"""{{
    admin 127.0.0.1:2019
    default_sni {TEST_HOST}
    storage file_system {{
        root {TEST_ROOT}/caddy-data
    }}
}}
https://{TEST_HOST} {{
    tls internal {{
        key_type rsa2048
        reuse_private_keys
    }}
    @hub path /api/* /ws/* /health /download/* /join/*
    handle @hub {{
        reverse_proxy 127.0.0.1:4000
    }}
    handle {{
        reverse_proxy 127.0.0.1:3000
    }}
}}
""")
    # Run Caddy as the SAME unprivileged fixture user so caddy-data (incl. the
    # internal CA private key) stays 0600 user-owned; the hub, being the same
    # user, reads root.crt naturally with no world-readable chmod. Ambient
    # CAP_NET_BIND_SERVICE lets the non-root process bind :443.
    write("/etc/systemd/system/caddy.service", f"""[Unit]
After=network.target
[Service]
User={user.pw_name}
Environment=HOME={hub_home}
ExecStart=/usr/bin/env caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=always
[Install]
WantedBy=multi-user.target
""")
    # The outbox must EXIST and be traversable/readable by the unprivileged hub
    # BEFORE it starts, or the candidate hub never sees the maintenance marker
    # the engine writes there — which would make the gate a false pass. The
    # worker's umask (0077) would otherwise create it 0700-root.
    outbox = TEST_ROOT / "mailbox" / "outbox"
    outbox.mkdir(parents=True, exist_ok=True)
    os.chmod(TEST_ROOT / "mailbox", 0o755)
    os.chmod(outbox, 0o755)
    # Caddy runs as the fixture user; pre-create its storage user-owned 0700 so
    # the internal CA and its private key never leave that user.
    caddy_data = TEST_ROOT / "caddy-data"
    caddy_data.mkdir(parents=True, exist_ok=True)
    os.chown(caddy_data, user.pw_uid, user.pw_gid)
    os.chmod(caddy_data, 0o700)
    if HOSTS_LINE not in Path("/etc/hosts").read_text():
        with open("/etc/hosts", "a") as fh:
            fh.write(HOSTS_LINE)
        CREATED["hosts_line"] = True
    sh("systemctl", "daemon-reload")
    for unit in UNITS:
        sh("systemctl", "enable", "--now", unit)


def build_config():
    return {
        "mode": "native", "public_url": f"https://{TEST_HOST}", "ca_file": str(caddy_root()),
        "mailbox_dir": str(TEST_ROOT / "mailbox"), "state_dir": str(TEST_ROOT / "state"),
        "hub_unit": "bloxos-hub.service", "dashboard_unit": "bloxos-dashboard.service",
        "proxy_unit": "caddy.service",
        "hub_binary": str(TEST_ROOT / "releases/original/hub/bloxos-hub"),
        "hub_workdir": str(TEST_ROOT / "hub-workdir"), "hub_home": str(TEST_ROOT / "home"),
        "node_binary": shutil.which("node"),
        "hub_url": "http://127.0.0.1:4000", "dashboard_url": "http://127.0.0.1:3000",
        "systemd_dir": "/etc/systemd/system", "releases_dir": str(TEST_ROOT / "releases"),
    }


def local_fetch(candidate_dir, manifest):
    entry = manifest["native"][native.current_arch()]
    asset_url = engine.RELEASE_ASSET.format(version=manifest["version"], file=entry["file"])
    asset_bytes = (Path(candidate_dir) / entry["file"]).read_bytes()
    manifest_bytes = json.dumps(manifest).encode()

    def fetch(url, max_bytes):
        if url == engine.RELEASE_LATEST:
            return manifest_bytes
        if url == asset_url:
            return asset_bytes
        raise engine.UpdaterError("unexpected fetch URL in smoke fixture: " + url)
    return fetch


def run_transaction(config, fetch):
    # Retire the previous (finished) transaction, retaining its backup, exactly
    # like the worker — never destroy a prior backup.
    engine.archive_completed_transaction(config["state_dir"])
    tx = os.path.join(config["state_dir"], "transaction")
    os.makedirs(tx, exist_ok=True)
    adapter = native.NativeAdapter(config, tx, runner=native.default_runner, fetch_bytes=fetch)
    cfg = engine.Config.from_dict(config)
    return engine.Transaction(cfg, adapter, fetch_bytes=fetch).apply(str(uuid.uuid4()))


def wait_health():
    """Liveness only — works on a build-info-less original."""
    import ssl
    import urllib.request
    deadline = time.monotonic() + 150
    while True:
        try:
            ctx = ssl.create_default_context(cafile=str(caddy_root()))
            with urllib.request.urlopen(f"https://{TEST_HOST}/health", timeout=10, context=ctx) as resp:
                if resp.status == 200 and json.load(resp).get("status") == "ok":
                    return
        except Exception:
            pass
        if time.monotonic() >= deadline:
            die("deployment never became reachable behind Caddy")
        time.sleep(3)


def public_identity():
    return engine.default_identity_fetch(f"https://{TEST_HOST}", "hub", str(caddy_root()), 10.0)


def assert_intact(user):
    assert read_fixture_row() == "SEEDED", "sqlite fixture row lost/changed"
    assert (TEST_ROOT / "hub-workdir/.kyzn-secret").read_text() == "do-not-delete", "untracked lost"
    assert (TEST_ROOT / "operator-source/README.md").read_text() == "operator checkout", "source touched"
    assert db_owner() == (user.pw_uid, user.pw_gid), "DB ownership changed (left root-owned?)"


def teardown():
    for unit in UNITS:
        subprocess.run(["systemctl", "disable", "--now", unit], capture_output=True)
        with contextlib.suppress(FileNotFoundError):
            os.remove(f"/etc/systemd/system/{unit}")
    # Only our own managed drop-ins (created by NativeAdapter.install), never a
    # pre-existing operator drop-in.
    for unit in ("bloxos-hub.service", "bloxos-dashboard.service"):
        d = Path("/etc/systemd/system", unit + ".d", native.DROPIN_NAME)
        with contextlib.suppress(FileNotFoundError):
            os.remove(d)
        with contextlib.suppress(OSError):
            os.rmdir(d.parent)
    subprocess.run(["systemctl", "daemon-reload"], capture_output=True)
    with contextlib.suppress(FileNotFoundError):
        os.remove(CADDYFILE)
    if CREATED["caddy_dir"]:
        with contextlib.suppress(OSError):
            os.rmdir(CADDYFILE.parent)
    if CREATED["hosts_line"]:
        with contextlib.suppress(OSError):
            hosts = Path("/etc/hosts").read_text().replace(HOSTS_LINE, "")
            Path("/etc/hosts").write_text(hosts)
    engine._remove_tree(str(TEST_ROOT))


def main():
    global TEST_ROOT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--original-bundle", required=True)
    parser.add_argument("--candidate-bundle", required=True)
    parser.add_argument("--state-root", default=str(TEST_ROOT),
                        help="dedicated fresh root (must not exist)")
    parser.add_argument("--keep", action="store_true", help="skip teardown for inspection")
    args = parser.parse_args()
    TEST_ROOT = Path(args.state_root)
    require_disposable(args)

    user = unprivileged_user()
    try:
        TEST_ROOT.mkdir(mode=0o755, parents=True, exist_ok=False)
        extract_bundle(args.original_bundle, TEST_ROOT / "releases/original")
        # Seed the sqlite fixture + user files BEFORE the hub opens the DB.
        (TEST_ROOT / "hub-workdir").mkdir(parents=True, exist_ok=True)
        os.chown(TEST_ROOT / "hub-workdir", user.pw_uid, user.pw_gid)
        seed_db_as_user(user)
        untracked = TEST_ROOT / "hub-workdir/.kyzn-secret"
        untracked.write_text("do-not-delete")
        os.chown(untracked, user.pw_uid, user.pw_gid)
        write(TEST_ROOT / "operator-source/README.md", "operator checkout")

        install_original(TEST_ROOT / "releases/original", user)
        wait_health()  # readiness without requiring build-info from the original

        config = build_config()
        candidate_manifest = json.loads((Path(args.candidate_bundle) / "update-manifest.json").read_text())

        # 1. SUCCESS -----------------------------------------------------------
        state = run_transaction(config, local_fetch(args.candidate_bundle, candidate_manifest))
        assert state == engine.SUCCEEDED, f"success case ended {state}"
        pub = public_identity()
        assert (pub["version"], pub["revision"]) == (candidate_manifest["version"], candidate_manifest["revision"]), \
            f"public endpoint not serving candidate: {pub}"
        assert_intact(user)
        pre_second = (pub["version"], pub["revision"])  # rollback-2 must return HERE
        print("PASS success: public endpoint serves the candidate; data + source + ownership intact")

        # 2. WRONG-REVISION ROLLBACK ------------------------------------------
        tampered = dict(candidate_manifest, revision="deadbeef" * 5)  # 40 hex, != real
        state = run_transaction(config, local_fetch(args.candidate_bundle, tampered))
        assert state == engine.ROLLED_BACK, f"rollback case ended {state}"
        restored = public_identity()
        assert (restored["version"], restored["revision"]) == pre_second, \
            f"rollback did not restore the pre-transaction (candidate) state: {restored}"
        assert_intact(user)
        print("PASS rollback: revision mismatch rolled back to the candidate; data + ownership intact")

        print("ALL NATIVE SMOKE CHECKS PASSED")
    finally:
        if not args.keep:
            teardown()


if __name__ == "__main__":
    main()
