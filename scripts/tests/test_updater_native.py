#!/usr/bin/env python3
"""Offline tests for the native adapter: non-destructive drop-in switch,
ownership-preserving DB restore, exact drop-in rollback, strict (non-silent)
stop/start failures, ELF/arch + dashboard validation on staging, and
evidence-grounded discover_native routing proof. No real systemd/network."""
import io
import json
import os
import sys
import tarfile
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from updater import engine, native  # noqa: E402


def fake_elf(arch):
    machine = {"amd64": 0x3E, "arm64": 0xB7}[arch]
    b = bytearray(64)
    b[0:4] = b"\x7fELF"
    b[4] = 2  # 64-bit
    b[5] = 1  # little-endian
    b[18] = machine & 0xFF
    b[19] = (machine >> 8) & 0xFF
    return bytes(b)


def make_bundle_tar(path, arch="amd64", with_server=True, with_next=True):
    with tarfile.open(path, "w:gz") as tar:
        def add(name, data):
            info = tarfile.TarInfo(name)
            info.size = len(data)
            tar.addfile(info, io.BytesIO(data))
        add("hub/bloxos-hub", fake_elf(arch))
        if with_server:
            add("dashboard/server.js", b"// server")
        if with_next:
            add("dashboard/.next/BUILD_ID", b"abc")


class Runner:
    """Records systemctl calls; fails those whose (verb, unit) is in `fail`."""

    def __init__(self, fail=()):
        self.calls = []
        self.fail = set(fail)

    def __call__(self, cmd):
        self.calls.append(cmd)
        if cmd[:1] == ["systemctl"] and len(cmd) >= 2:
            key = (cmd[1], cmd[-1] if len(cmd) > 2 else "")
            if key in self.fail or (cmd[1], "*") in self.fail:
                return 1, "", "boom"
        return 0, "", ""


def native_config(tmp, **over):
    hub_workdir = os.path.join(tmp, "hub-workdir")
    hub_home = os.path.join(tmp, "home")
    os.makedirs(hub_workdir, exist_ok=True)
    os.makedirs(os.path.join(hub_home, ".bloxos"), exist_ok=True)
    node = os.path.join(tmp, "node")
    open(node, "w").write("#!/bin/sh\n")
    cfg = {
        "mode": "native", "public_url": "https://hub.example", "ca_file": None,
        "mailbox_dir": os.path.join(tmp, "mailbox"), "state_dir": os.path.join(tmp, "state"),
        "hub_unit": "bloxos-hub.service", "dashboard_unit": "bloxos-dashboard.service",
        "proxy_unit": "caddy.service",
        "hub_binary": os.path.join(tmp, "orig-hub"), "hub_workdir": hub_workdir,
        "hub_home": hub_home, "node_binary": node,
        "hub_url": "http://127.0.0.1:4000", "dashboard_url": "http://127.0.0.1:3000",
        "systemd_dir": os.path.join(tmp, "systemd"), "releases_dir": os.path.join(tmp, "releases"),
    }
    cfg.update(over)
    open(cfg["hub_binary"], "w").write("original-hub-binary")
    os.makedirs(cfg["systemd_dir"], exist_ok=True)
    return cfg


def manifest(arch="amd64"):
    return {"schema": 1, "version": "v1.2.3", "revision": "a" * 40, "images": {},
            "native": {arch: {"file": f"bloxos-server-linux-{arch}.tar.gz", "sha256": "x"}}}


def adapter_with_bundle(tmp, cfg, runner, arch="amd64", **bundle):
    """Build a NativeAdapter whose fetch serves a real bundle tar for this arch."""
    tarpath = os.path.join(tmp, "bundle.tar.gz")
    make_bundle_tar(tarpath, arch=arch, **bundle)
    data = open(tarpath, "rb").read()
    import hashlib
    m = manifest(arch)
    m["native"][arch]["sha256"] = hashlib.sha256(data).hexdigest()
    tdir = os.path.join(cfg["state_dir"], "transaction")
    os.makedirs(tdir, exist_ok=True)
    ad = native.NativeAdapter(cfg, tdir, runner=runner, ready_check=lambda url: None,
                              fetch_bytes=lambda url, mx: data, arch=arch)
    return ad, m


class StageValidationTests(unittest.TestCase):
    def test_stage_publishes_and_validates(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            ad, m = adapter_with_bundle(tmp, cfg, Runner())
            ad.stage(m, os.path.join(cfg["state_dir"], "transaction", "release"))
            release = ad.journal.get("release_dir")
            self.assertTrue(os.path.isfile(os.path.join(release, "hub", "bloxos-hub")))
            self.assertTrue(os.path.isfile(os.path.join(release, "dashboard", "server.js")))

    def test_wrong_arch_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            # bundle built for arm64 but adapter expects amd64
            ad, m = adapter_with_bundle(tmp, cfg, Runner(), arch="amd64")
            # rebuild the tar as arm64 ELF while manifest/adapter say amd64
            tarpath = os.path.join(tmp, "bad.tar.gz")
            make_bundle_tar(tarpath, arch="arm64")
            data = open(tarpath, "rb").read()
            import hashlib
            m["native"]["amd64"]["sha256"] = hashlib.sha256(data).hexdigest()
            ad._fetch = lambda url, mx: data
            with self.assertRaises(engine.UpdaterError):
                ad.stage(m, os.path.join(cfg["state_dir"], "transaction", "release2"))

    def test_missing_server_js_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            ad, m = adapter_with_bundle(tmp, cfg, Runner(), with_server=False)
            with self.assertRaises(engine.UpdaterError):
                ad.stage(m, os.path.join(cfg["state_dir"], "transaction", "release"))


class NonDestructiveInstallTests(unittest.TestCase):
    def test_install_never_touches_user_dirs_and_switches_via_dropins(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            # Plant user files that MUST survive: a checkout marker in hub_workdir
            # and a DB, plus an untracked file.
            db = os.path.join(cfg["hub_workdir"], "bloxos.db")
            open(db, "w").write("USERDATA")
            untracked = os.path.join(cfg["hub_workdir"], ".kyzn-secret")
            open(untracked, "w").write("keep me")
            runner = Runner()
            ad, m = adapter_with_bundle(tmp, cfg, runner)
            ad.stage(m, os.path.join(cfg["state_dir"], "transaction", "release"))
            ad.quiesce()
            ad.backup()
            ad.install()
            # User data untouched.
            self.assertEqual(open(db).read(), "USERDATA")
            self.assertTrue(os.path.isfile(untracked))
            # The original hub binary file is NOT modified by install (drop-in
            # points elsewhere).
            self.assertEqual(open(cfg["hub_binary"]).read(), "original-hub-binary")
            # Drop-ins written for hub + dashboard; hub drop-in has NO
            # WorkingDirectory (DB stays); dashboard drop-in runs node server.js.
            hub_dropin = os.path.join(cfg["systemd_dir"], "bloxos-hub.service.d", native.DROPIN_NAME)
            dash_dropin = os.path.join(cfg["systemd_dir"], "bloxos-dashboard.service.d", native.DROPIN_NAME)
            self.assertIn("ExecStart=", open(hub_dropin).read())
            self.assertNotIn("WorkingDirectory", open(hub_dropin).read())
            self.assertIn("server.js", open(dash_dropin).read())
            self.assertIn("WorkingDirectory=", open(dash_dropin).read())
            self.assertIn(["systemctl", "daemon-reload"], runner.calls)

    def test_quiesce_stops_proxy_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            runner = Runner()
            ad, _ = adapter_with_bundle(tmp, cfg, runner)
            ad.quiesce()
            stops = [c for c in runner.calls if c[1] == "stop"]
            self.assertEqual(stops[0][-1], "caddy.service")  # proxy first


class BackupRollbackTests(unittest.TestCase):
    def _staged_backed(self, tmp, cfg, runner):
        ad, m = adapter_with_bundle(tmp, cfg, runner)
        ad.stage(m, os.path.join(cfg["state_dir"], "transaction", "release"))
        ad.quiesce()
        ad.backup()
        return ad

    def test_rollback_restores_dropins_db_and_ownership(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            db = os.path.join(cfg["hub_workdir"], "bloxos.db")
            open(db, "w").write("ORIGINAL-DB")
            # a pre-existing hub drop-in that must be restored byte-exact
            hub_d_dir = os.path.join(cfg["systemd_dir"], "bloxos-hub.service.d")
            os.makedirs(hub_d_dir)
            prior = os.path.join(hub_d_dir, native.DROPIN_NAME)
            open(prior, "w").write("[Service]\nEnvironment=OLD=1\n")
            os.chmod(prior, 0o644)
            runner = Runner()
            ad = self._staged_backed(tmp, cfg, runner)
            ad.install()  # overwrites the hub drop-in and writes a dashboard one
            # simulate migration: DB changed after install
            open(db, "w").write("MIGRATED-DB")
            ad.rollback()
            # DB restored to the pre-install snapshot
            self.assertEqual(open(db).read(), "ORIGINAL-DB")
            # prior hub drop-in restored byte-exact
            self.assertEqual(open(prior).read(), "[Service]\nEnvironment=OLD=1\n")
            # dashboard drop-in (which did NOT exist before) removed
            dash_dropin = os.path.join(cfg["systemd_dir"], "bloxos-dashboard.service.d", native.DROPIN_NAME)
            self.assertFalse(os.path.exists(dash_dropin))

    def test_rollback_stop_failure_is_not_silent(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            open(os.path.join(cfg["hub_workdir"], "bloxos.db"), "w").write("db")
            # hub stop fails -> rollback must raise, never restore under a live hub
            runner = Runner(fail=[("stop", "bloxos-hub.service")])
            ad = self._staged_backed(tmp, cfg, Runner())
            ad.install()
            ad._runner = runner  # swap in the failing runner for rollback
            with self.assertRaises(engine.UpdaterError):
                ad.rollback()

    def test_start_original_not_ready_raises(self):
        # systemctl start "succeeding" is not proof: if the restarted original
        # never answers, rollback must fail loudly, not claim success.
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            open(os.path.join(cfg["hub_workdir"], "bloxos.db"), "w").write("db")
            tdir = os.path.join(cfg["state_dir"], "transaction")
            os.makedirs(tdir, exist_ok=True)

            def never_ready(url):
                raise engine.UpdaterError("down")
            ad = native.NativeAdapter(cfg, tdir, runner=Runner(),
                                      ready_check=never_ready, readiness_timeout=0.05,
                                      poll_interval=0.0, sleep=lambda _s: None,
                                      fetch_bytes=lambda u, m: b"", discover=lambda: {})
            with self.assertRaises(engine.UpdaterError):
                ad.resume_original()

    def test_resume_original_starts_without_restore(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            runner = Runner()
            ad, m = adapter_with_bundle(tmp, cfg, runner)
            ad.quiesce()
            ad.resume_original()
            starts = [c for c in runner.calls if c[1] == "start"]
            self.assertEqual({c[-1] for c in starts},
                             {"bloxos-hub.service", "bloxos-dashboard.service", "caddy.service"})


class ElfTests(unittest.TestCase):
    def test_elf_arch_check(self):
        with tempfile.TemporaryDirectory() as tmp:
            good = os.path.join(tmp, "amd64")
            open(good, "wb").write(fake_elf("amd64"))
            native._verify_elf_arch(good, "amd64")
            with self.assertRaises(engine.UpdaterError):
                native._verify_elf_arch(good, "arm64")
            notelf = os.path.join(tmp, "x")
            open(notelf, "wb").write(b"MZ not elf" + b"\x00" * 40)
            with self.assertRaises(engine.UpdaterError):
                native._verify_elf_arch(notelf, "amd64")


class PreflightOwnershipTests(unittest.TestCase):
    def _adapter(self, tmp, disc):
        cfg = native_config(tmp)
        tdir = os.path.join(cfg["state_dir"], "transaction")
        os.makedirs(tdir, exist_ok=True)
        return native.NativeAdapter(cfg, tdir, runner=Runner(), discover=lambda: disc, ready_check=lambda u: None), cfg

    def _matching_disc(self, cfg, **over):
        d = {"hub_unit": cfg["hub_unit"], "dashboard_unit": cfg["dashboard_unit"],
             "proxy_unit": cfg["proxy_unit"], "hub_workdir": cfg["hub_workdir"],
             "hub_home": cfg["hub_home"], "public_url": cfg["public_url"],
             "routing_verified": True, "routing_reason": "ok"}
        d.update(over)
        return d

    def test_passes_when_ownership_reconfirmed(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            tdir = os.path.join(cfg["state_dir"], "transaction")
            os.makedirs(tdir, exist_ok=True)
            disc = self._matching_disc(cfg)
            ad = native.NativeAdapter(cfg, tdir, runner=Runner(), discover=lambda: disc, ready_check=lambda u: None)
            ad.preflight()  # no raise

    def test_dropin_changed_binaries_tolerated(self):
        # A prior update makes discovery report the RELEASE binary/node and a
        # different dashboard_workdir — preflight must still pass (stable
        # identity unchanged, edge still ours).
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            tdir = os.path.join(cfg["state_dir"], "transaction")
            os.makedirs(tdir, exist_ok=True)
            disc = self._matching_disc(cfg, hub_binary="/var/lib/bloxos-updater/releases/x/hub/bloxos-hub",
                                       node_binary="/other/node",
                                       dashboard_workdir="/var/lib/bloxos-updater/releases/x/dashboard")
            ad = native.NativeAdapter(cfg, tdir, runner=Runner(), discover=lambda: disc, ready_check=lambda u: None)
            ad.preflight()  # tolerated

    def test_fails_on_ambiguous(self):
        with tempfile.TemporaryDirectory() as tmp:
            ad, _ = self._adapter(tmp, {"ambiguous": "custom layout"})
            with self.assertRaises(engine.UpdaterError):
                ad.preflight()

    def test_fails_on_moved_db_workdir(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            tdir = os.path.join(cfg["state_dir"], "transaction")
            os.makedirs(tdir, exist_ok=True)
            disc = self._matching_disc(cfg, hub_workdir="/somewhere/else")
            ad = native.NativeAdapter(cfg, tdir, runner=Runner(), discover=lambda: disc, ready_check=lambda u: None)
            with self.assertRaises(engine.UpdaterError):
                ad.preflight()

    def test_fails_when_routing_unverified(self):
        with tempfile.TemporaryDirectory() as tmp:
            cfg = native_config(tmp)
            tdir = os.path.join(cfg["state_dir"], "transaction")
            os.makedirs(tdir, exist_ok=True)
            disc = self._matching_disc(cfg, routing_verified=False, routing_reason="cert mismatch")
            ad = native.NativeAdapter(cfg, tdir, runner=Runner(), discover=lambda: disc, ready_check=lambda u: None)
            with self.assertRaises(engine.UpdaterError):
                ad.preflight()


class DiscoverTests(unittest.TestCase):
    def _seams(self, *, caddy_ok=True, cert_match=True,
               dash_owner="bloxos-dashboard.service", caddy443_owner="caddy.service",
               hub_env=None):
        env = {"HOME": "/home/bloxi", "PUBLIC_URL": "https://hub.example",
               "BLOXOS_CA_CERT": "/etc/ca.crt"} if hub_env is None else hub_env
        control_groups = {"bloxos-hub.service": "/system.slice/bloxos-hub.service",
                          "bloxos-dashboard.service": "/system.slice/bloxos-dashboard.service",
                          "caddy.service": "/system.slice/caddy.service"}
        show = lambda unit: {"ActiveState": "active",
                             "MainPID": {"bloxos-hub.service": "10", "bloxos-dashboard.service": "20",
                                         "caddy.service": "30"}.get(unit, "0"),
                             "ControlGroup": control_groups.get(unit, "/system.slice/" + unit)}

        def read_proc(pid):
            # The dashboard listener is pid 21 (a pnpm->node CHILD of MainPID 20).
            if pid == 10:
                return {"exe": "/opt/bloxos/hub/bloxos-hub", "cwd": "/opt/bloxos/hub", "environ": env}
            if pid == 21:
                return {"exe": "/usr/bin/node", "cwd": "/opt/bloxos/dashboard", "environ": {}}
            return {"exe": "/usr/bin/other", "cwd": "/x", "environ": {}}

        listeners = lambda: [
            {"port": 4000, "addr": "127.0.0.1", "pid": 10, "exe": "/opt/bloxos/hub/bloxos-hub", "cwd": "/opt/bloxos/hub"},
            {"port": 3000, "addr": "127.0.0.1", "pid": 21, "exe": "/usr/bin/node", "cwd": "/opt/bloxos/dashboard"},
            {"port": 443, "addr": "0.0.0.0", "pid": 31, "exe": "/usr/bin/caddy", "cwd": "/"},
            {"port": 2019, "addr": "127.0.0.1", "pid": 31, "exe": "/usr/bin/caddy", "cwd": "/"},
        ]
        # v2 cgroup lines; pid 21 is a child in the dashboard (or foreign) slice.
        cgroups = {10: "0::/system.slice/bloxos-hub.service",
                   21: "0::/system.slice/" + dash_owner + "/pnpm-child.scope",
                   31: "0::/system.slice/" + caddy443_owner}
        read_cgroup = lambda pid: cgroups.get(int(pid), "0::/system.slice/other.service")

        def caddy_config():
            if not caddy_ok:
                return {"apps": {}}
            return {"apps": {"http": {"servers": {"s": {"routes": [
                {"handle": [{"handler": "reverse_proxy",
                             "upstreams": [{"dial": "127.0.0.1:4000"}, {"dial": "127.0.0.1:3000"}]}]}]}}}}}

        def leaf_cert(host, port, sni, ca):
            return b"CERT-PUBLIC" if (host != "127.0.0.1") else (b"CERT-PUBLIC" if cert_match else b"CERT-LOCAL")

        return dict(show=show, read_proc=read_proc, listeners=listeners,
                    caddy_config=caddy_config, leaf_cert=leaf_cert, read_cgroup=read_cgroup)

    def test_discover_verified(self):
        r = native.discover_native(**self._seams())
        self.assertNotIn("ambiguous", r)
        self.assertEqual(r["public_url"], "https://hub.example")
        self.assertEqual(r["ca_file"], "/etc/ca.crt")
        self.assertEqual(r["hub_binary"], "/opt/bloxos/hub/bloxos-hub")
        self.assertEqual(r["node_binary"], "/usr/bin/node")
        self.assertEqual(r["dashboard_workdir"], "/opt/bloxos/dashboard")
        self.assertTrue(r["routing_verified"])

    def test_cert_mismatch_not_verified(self):
        r = native.discover_native(**self._seams(cert_match=False))
        self.assertFalse(r["routing_verified"])
        self.assertIn("certificate", r["routing_reason"])

    def test_caddy_not_routing_not_verified(self):
        r = native.discover_native(**self._seams(caddy_ok=False))
        self.assertFalse(r["routing_verified"])

    def test_no_public_url_is_ambiguous(self):
        r = native.discover_native(**self._seams(hub_env={"HOME": "/h"}))
        self.assertIn("ambiguous", r)

    def test_foreign_node_listener_ambiguous(self):
        # A node loopback listener that is NOT in the dashboard unit's cgroup
        # (e.g. some other service) must never be selected as the dashboard.
        r = native.discover_native(**self._seams(dash_owner="some-other.service"))
        self.assertIn("ambiguous", r)

    def test_foreign_caddy_443_ambiguous(self):
        # The :443 listener not owned by the selected proxy unit fails the edge
        # ownership check.
        r = native.discover_native(**self._seams(caddy443_owner="nginx.service"))
        self.assertIn("ambiguous", r)


if __name__ == "__main__":
    unittest.main()
