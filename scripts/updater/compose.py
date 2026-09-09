"""Update one explicitly configured local Compose deployment, never its neighbours."""
import json
import copy
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import time
import socket
import ssl
from urllib.parse import urlsplit


def run(args, **kwargs):
    result = subprocess.run(args, capture_output=True, timeout=600, **kwargs)
    if result.returncode:
        # Compose output can contain rendered secrets. Keep it out of web status.
        raise RuntimeError(f"{args[0]} operation failed (exit {result.returncode}); inspect host service logs")
    return result.stdout


def atomic_json(path, value):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix="." + path.name + "-", dir=path.parent)
    temporary = Path(name)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        temporary.unlink(missing_ok=True)


class ComposeAdapter:
    def __init__(self, config, transaction_dir):
        self.config = config
        self.tx = Path(transaction_dir)
        self.journal = self.tx / "compose.json"
        self.override = Path(config.get("compose_override", "/etc/bloxos-updater/compose.override.json"))
        self.saved = json.loads(self.journal.read_text()) if self.journal.exists() else {}

    def command(self, *args):
        base = ["docker", "compose", "--project-directory", self.config["compose_dir"],
                "--project-name", self.config["compose_project"]]
        for file in self.config["compose_files"]:
            base += ["--file", file]
        if self.override.exists():
            base += ["--file", str(self.override)]
        return base + list(args)

    def save(self):
        atomic_json(self.journal, self.saved)

    def containers(self):
        found = {}
        for service in ("hub", "dashboard", "caddy"):
            ids = run(self.command("ps", "--all", "--quiet", service), text=True).split()
            if len(ids) != 1:
                raise RuntimeError(f"Expected one existing {service} container; refusing to create another installation")
            found[service] = json.loads(run(["docker", "inspect", ids[0]], text=True))[0]
        return found

    def preflight(self):
        context = json.loads(run(["docker", "context", "inspect"], text=True))[0]
        endpoint = os.environ.get("DOCKER_HOST") or context["Endpoints"]["docker"]["Host"]
        if endpoint not in ("unix:///var/run/docker.sock", "unix:///run/docker.sock"):
            raise RuntimeError("The updater supports the standard local Docker daemon only")
        current = self.containers()
        for name, container in current.items():
            if not container["State"]["Running"]:
                raise RuntimeError(f"Existing {name} is not running; repair the deployment before updating")
            labels = container["Config"].get("Labels") or {}
            if labels.get("com.docker.compose.project") != self.config["compose_project"]:
                raise RuntimeError("Compose project identity does not match configuration")
        bindings = current["caddy"].get("NetworkSettings", {}).get("Ports", {}).get("443/tcp")
        if not bindings or not any(b["HostPort"] == "443" for b in bindings):
            raise RuntimeError("Selected Caddy does not publish HTTPS on this host; unsupported proxy layout")
        hub_env = dict(value.split("=", 1) for value in current["hub"]["Config"].get("Env", []) if "=" in value)
        if hub_env.get("PUBLIC_URL", "").rstrip("/") != self.config["public_url"].rstrip("/"):
            raise RuntimeError("The selected hub has a different public URL")
        self.verify_edge()
        volumes = {}
        for service, target in (("hub", "/data"), ("caddy", "/data"), ("caddy", "/config")):
            match = [m for m in current[service]["Mounts"] if m["Destination"] == target]
            if len(match) != 1 or match[0]["Type"] != "volume" or not match[0].get("Name"):
                raise RuntimeError("Automatic Compose updates require the standard named data volumes")
            volumes[service + target.replace("/", "-")] = match[0]["Name"]
        # Validate rendered config reaches the same persistent identity BEFORE up.
        rendered = json.loads(run(self.command("config", "--format", "json"), text=True))
        for service, target in (("hub", "/data"), ("caddy", "/data"), ("caddy", "/config")):
            entry = next((v for v in rendered["services"][service].get("volumes", []) if v["target"] == target), None)
            source = entry.get("source") if entry else None
            volume = rendered.get("volumes", {}).get(source, {}).get("name")
            if volume != volumes[service + target.replace("/", "-")]:
                raise RuntimeError("Compose config points at different data; refusing installation switch")
        if not self.saved:
            self.saved = {"containers": {k: v["Id"] for k, v in current.items()},
                          "images": {k: v["Image"] for k, v in current.items()},
                          "restart_policies": {k: v["HostConfig"]["RestartPolicy"] for k, v in current.items()},
                          "volumes": volumes,
                          "override": json.loads(self.override.read_text()) if self.override.exists() else None}
            self.save()

    def verify_edge(self):
        """Even old hubs need concrete edge ownership, not a health-only guess.

        Match the selected Caddy TLS endpoint to the public certificate and
        inspect its ACTIVE proxy configuration, not merely a file on disk.
        This deliberately supports the standard local Compose edge only.
        """
        public = urlsplit(self.config["public_url"])
        if public.scheme != "https" or not public.hostname or public.port not in (None, 443) or public.path not in ("", "/"):
            raise RuntimeError("Unsupported public URL for the standard Compose edge")
        active = json.loads(run(self.command("exec", "-T", "caddy", "wget", "-qO-", "http://127.0.0.1:2019/config/"), text=True))
        upstreams = set()
        def visit(value):
            if isinstance(value, dict):
                if value.get("handler") == "reverse_proxy":
                    upstreams.update(entry.get("dial") for entry in value.get("upstreams", []))
                for child in value.values():
                    visit(child)
            elif isinstance(value, list):
                for child in value:
                    visit(child)
        visit(active)
        if upstreams != {"hub:4000", "dashboard:3000"}:
            raise RuntimeError("The active Caddy routes are not the supported hub/dashboard upstreams")
        ca = self.local_ca()
        context = ssl.create_default_context()
        if ca:
            context.load_verify_locations(cadata=ca.decode())
        def certificate(host):
            with socket.create_connection((host, 443), timeout=10) as connection:
                with context.wrap_socket(connection, server_hostname=public.hostname) as tls:
                    return tls.getpeercert(binary_form=True)
        if certificate("127.0.0.1") != certificate(public.hostname):
            raise RuntimeError("Public HTTPS is served by a different edge; refusing to update this stack")

    def local_ca(self):
        return run(self.command("exec", "-T", "caddy", "sh", "-c",
                   "if test -f /data/caddy/pki/authorities/local/root.crt; then cat /data/caddy/pki/authorities/local/root.crt; fi"))

    def stage(self, manifest, release_dir):
        del release_dir
        images = manifest["images"]
        for service in ("hub", "dashboard"):
            if not re.fullmatch(r"ghcr\.io/bokiko/bloxos-" + service + r"@sha256:[0-9a-f]{64}", images[service]):
                raise RuntimeError("Invalid release image reference")
            run(["docker", "pull", images[service]])
        self.saved["candidate"] = images
        self.save()

    def quiesce(self):
        # Docker may restart containers before the host worker on reboot. Pin
        # restart=no DURABLY before stopping/snapshotting so neither legacy nor
        # candidate processes can accept writes ahead of recovery.
        if not self.saved.get("restart_policies"):
            raise RuntimeError("Missing original restart policies; refusing unsafe downtime")
        self.saved["restart_inhibited"] = True
        self.save()
        current = self.containers()
        for name in ("caddy", "hub", "dashboard"):
            run(["docker", "update", "--restart=no", current[name]["Id"]])
        override = json.loads(self.override.read_text()) if self.override.exists() else {"services": {}}
        for name in ("caddy", "hub", "dashboard"):
            override.setdefault("services", {}).setdefault(name, {})["restart"] = "no"
        atomic_json(self.override, override)
        run(self.command("stop", "caddy"))
        run(self.command("stop", "hub", "dashboard"))

    def backup(self):
        for key in self.saved["volumes"]:
            service, target = key.split("-", 1)
            archive = self.tx / (key + ".tar")
            with archive.open("xb") as output:
                os.chmod(archive, 0o600)
                result = subprocess.run(["docker", "cp", self.saved["containers"][service] + ":/" + target + "/.", "-"],
                                        stdout=output, stderr=subprocess.PIPE, timeout=300)
                output.flush()
                os.fsync(output.fileno())
            if result.returncode:
                raise RuntimeError("Stopped-volume backup failed; no new binaries installed")
            with tarfile.open(archive) as tar:
                for member in tar:
                    if member.name.startswith("/") or ".." in Path(member.name).parts:
                        raise RuntimeError("Unsafe backup archive")
        self.saved["backup_complete"] = True
        self.save()

    def install(self):
        override = copy.deepcopy(self.saved["override"]) or {"services": {}}
        for name in ("hub", "dashboard"):
            override.setdefault("services", {}).setdefault(name, {})["image"] = self.saved["candidate"][name]
        for name in ("hub", "dashboard", "caddy"):
            override.setdefault("services", {}).setdefault(name, {})["restart"] = "no"
        atomic_json(self.override, override)

    def start_candidate(self):
        run(self.command("up", "-d", "--no-build", "--pull", "never", "--no-deps", "hub", "dashboard"))
        run(self.command("start", "caddy"))

    def identities(self):
        result = {}
        for service, port, path in (("hub", 4000, "/api/build-info"), ("dashboard", 3000, "/build-info")):
            raw = run(self.command("exec", "-T", service, "wget", "-qO-", f"http://127.0.0.1:{port}{path}"), text=True)
            result[service] = json.loads(raw)
        return result

    def resume_original(self):
        # No install was attempted: restart the unchanged containers only.
        run(self.command("start", "hub", "dashboard"))
        self.wait_original()

    def rollback(self):
        if not self.saved.get("backup_complete"):
            raise RuntimeError("No complete snapshot; refusing an unsafe partial rollback")
        self.quiesce()
        for key, volume in self.saved["volumes"].items():
            if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]+", volume):
                raise RuntimeError("Invalid saved volume identity")
            # Only the exact named BloxOS volume recorded before mutation is
            # mounted. Proxy/components are stopped, so no accepted writes are lost.
            with (self.tx / (key + ".tar")).open("rb") as source:
                result = subprocess.run(["docker", "run", "--rm", "-i", "--network", "none", "--user", "0",
                                         "--volume", volume + ":/restore", "--entrypoint", "/bin/sh",
                                         self.saved["images"]["hub"], "-c",
                                         "find /restore -mindepth 1 -maxdepth 1 -exec rm -rf -- {} + && tar -xf - -C /restore"],
                                        stdin=source, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=300)
                if result.returncode:
                    raise RuntimeError("Volume restore failed; services remain stopped for recovery")
        override = copy.deepcopy(self.saved["override"]) or {"services": {}}
        for name in ("hub", "dashboard"):
            override.setdefault("services", {}).setdefault(name, {})["image"] = self.saved["images"][name]
        for name in ("hub", "dashboard", "caddy"):
            override.setdefault("services", {}).setdefault(name, {})["restart"] = "no"
        atomic_json(self.override, override)
        self.start_original()

    def start_original(self):
        run(self.command("up", "-d", "--no-build", "--pull", "never", "--no-deps", "hub", "dashboard"))
        self.wait_original()

    @staticmethod
    def restart_argument(policy):
        name = policy.get("Name") or "no"
        if name not in ("no", "always", "unless-stopped", "on-failure"):
            raise RuntimeError("Invalid saved restart policy")
        retries = policy.get("MaximumRetryCount", 0)
        if not isinstance(retries, int) or retries < 0:
            raise RuntimeError("Invalid saved restart retry count")
        return f"on-failure:{retries}" if name == "on-failure" and retries else name

    def finalize(self):
        if not self.saved.get("restart_inhibited"):
            return
        # Called only AFTER the transaction's terminal phase is fsynced. This
        # is safe to retry after any interruption without re-restoring data.
        current = self.containers()
        override = json.loads(self.override.read_text())
        for name in ("hub", "dashboard", "caddy"):
            policy = self.restart_argument(self.saved["restart_policies"][name])
            run(["docker", "update", "--restart=" + policy, current[name]["Id"]])
            override.setdefault("services", {}).setdefault(name, {})["restart"] = policy
        atomic_json(self.override, override)
        run(self.command("start", "hub", "dashboard"))
        run(self.command("start", "caddy"))
        deadline = time.monotonic() + 120
        while True:
            try:
                self.verify_edge()
                return
            except (RuntimeError, OSError, ValueError):
                if time.monotonic() >= deadline:
                    raise RuntimeError("Committed deployment edge did not reopen; recovery state retained")
                time.sleep(3)

    def wait_original(self):
        deadline = time.monotonic() + 120
        while True:
            try:
                current = self.containers()
                for service, port, path in (("hub", 4000, "/health"), ("dashboard", 3000, "/login")):
                    if current[service]["Image"] != self.saved["images"][service]:
                        raise RuntimeError("Restored image does not match the original")
                    run(self.command("exec", "-T", service, "wget", "-qO-", f"http://127.0.0.1:{port}{path}"))
                return
            except (RuntimeError, OSError, ValueError):
                if time.monotonic() >= deadline:
                    raise RuntimeError("Original installation did not become ready; recovery state retained")
                time.sleep(3)
