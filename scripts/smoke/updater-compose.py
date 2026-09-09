#!/usr/bin/env python3
"""Destructive-to-fixture integration test. Run ONLY in a disposable Linux VM.

Requires a standard old Compose stack and an initialized host updater. Release
network resolution is injected in this test; production code has no custom URL
or unsigned/local artifact mode. Actual adapters, containers, volumes, TLS,
maintenance, backup, install and rollback are exercised unchanged.
"""
import json
import os
from pathlib import Path
import subprocess
import sqlite3
import ssl
import secrets
import sys
import urllib.request
import uuid

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from updater import engine
from updater.compose import ComposeAdapter, atomic_json, run


def main():
    if os.environ.get("SMOKE_CONFIRM_DISPOSABLE") != "1" or os.geteuid() != 0:
        raise SystemExit("Requires root and SMOKE_CONFIRM_DISPOSABLE=1 in a disposable VM")
    config = engine.Config.load("/etc/bloxos-updater/config.json")
    if config.public_url != "https://127.0.0.1" or config.mode != "compose":
        raise SystemExit("Fixture must be a loopback-only Compose deployment")
    images = {name: run(["docker", "image", "inspect", "--format", "{{.Id}}", "bloxos-updater-test-" + name], text=True).strip()
              for name in ("hub", "dashboard")}
    manifest = {"schema": 1, "version": os.environ.get("BLOXOS_EXPECT_VERSION", "v1.3.0"), "revision": os.environ.get("BLOXOS_EXPECT_REVISION", "4b96ad6564c24b089b30b41ee74557fd03e9aaa7"),
                "images": {name: "ghcr.io/bokiko/bloxos-" + name + "@sha256:" + "a" * 64 for name in images},
                "native": {"arm64": {"file": "bloxos-server-linux-arm64.tar.gz", "sha256": "a" * 64}}}
    bootstrap = ComposeAdapter(engine.as_config_dict(config), Path(config.state_dir) / "fixture")
    context = ssl.create_default_context(cafile=config.ca_file)
    def api(path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(config.public_url + path, data=data, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(request, context=context, timeout=10) as response:
            return json.load(response)
    if api("/api/setup/status").get("needs_setup"):
        token = run(bootstrap.command("exec", "-T", "hub", "cat", "/data/.bloxos/setup-token"), text=True).strip()
        api("/api/setup", {"setup_token": token, "username": "updater-smoke", "password": secrets.token_urlsafe(24), "pin": "1234"})
    hub_mount = next(m for m in bootstrap.containers()["hub"]["Mounts"] if m["Destination"] == "/data")
    database = Path(hub_mount["Source"]) / "bloxos.db"
    def users():
        with sqlite3.connect(database) as db:
            return db.execute("SELECT id, username, password_hash FROM users ORDER BY id").fetchall()
    original_users = users()
    assert original_users, "fixture must contain a real user"
    original_ca = Path(config.ca_file).read_bytes()

    class LocalFixtureAdapter(ComposeAdapter):
        def stage(self, manifest, release_dir):
            # Built fixture images, never an option exposed by the real worker.
            self.saved["candidate"] = images
            self.save()

    for expected in ("succeeded", "rolled_back"):
        engine.archive_completed_transaction(config.state_dir)
        adapter = LocalFixtureAdapter(engine.as_config_dict(config), Path(config.state_dir) / "transaction")
        before = adapter.containers()
        if expected == "rolled_back":
            manifest["revision"] = "b" * 40  # decisive identity mismatch AFTER candidate starts
        request_id = str(uuid.uuid4())
        mailbox = engine.Mailbox(config.mailbox_dir)
        transaction = engine.Transaction(config, adapter, fetch_bytes=lambda *_: json.dumps(manifest).encode())
        actual = transaction.apply(request_id)
        assert actual == expected, (actual, engine.read_status(config))
        assert not mailbox.maintenance_present()
        after = adapter.containers()
        assert {name: value["Mounts"] for name, value in before.items()} == {name: value["Mounts"] for name, value in after.items()}, "persistent mounts changed"
        for name in images:
            assert after[name]["Image"] == images[name], "incorrect candidate/rollback image"
        assert users() == original_users, "user credentials changed"
        ca = run(adapter.command("exec", "-T", "caddy", "cat", "/data/caddy/pki/authorities/local/root.crt"))
        assert ca == original_ca, "Caddy CA changed"
        print("PASS:", expected, "with preserved users, CA, mounts and no maintenance marker", flush=True)


if __name__ == "__main__":
    main()
