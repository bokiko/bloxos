"""One CLI for setup, requesting updates and the independently running worker."""
import argparse
import contextlib
import grp
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import uuid
import zipapp
import zipfile

from .compose import ComposeAdapter, atomic_json, run
from .engine import Config, Mailbox, UpdaterError, run_worker

CONFIG = Path("/etc/bloxos-updater/config.json")
ROOT = Path("/var/lib/bloxos-updater")
CODE = Path("/usr/local/lib/bloxos-updater/bloxos-update")
TERMINAL = {"succeeded", "rolled_back", "failed"}


def protected_directory(path, mode=0o700):
    path = Path(path)
    if path.is_symlink():
        raise RuntimeError("Refusing a symlinked updater directory")
    path.mkdir(parents=True, exist_ok=True, mode=mode)
    info = path.stat()
    if info.st_uid != 0 or info.st_mode & 0o022:
        raise RuntimeError("Updater configuration/state must be root-owned and not writable by other users")
    os.chmod(path, mode)


def write_file(path, body, mode=0o644):
    path = Path(path)
    if path.is_symlink():
        raise RuntimeError("Refusing a symlinked managed file")
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as stream:
        temporary = Path(stream.name)
        stream.write(body.encode() if isinstance(body, str) else body)
        stream.flush()
        os.fsync(stream.fileno())
    os.chmod(temporary, mode)
    os.replace(temporary, path)


def systemd_active(unit):
    result = subprocess.run(["systemctl", "is-active", unit], capture_output=True, text=True, timeout=15)
    if result.returncode not in (0, 3, 4):
        raise RuntimeError("Cannot determine native service state; refusing to guess")
    return result.stdout.strip() == "active"


def discover_compose(repo_dir):
    directory = Path(repo_dir)
    if (directory / "docker" / "compose.yaml").exists():
        directory /= "docker"
    files = [directory / "compose.yaml"]
    if not files[0].is_file():
        raise RuntimeError("Run setup from the existing BloxOS checkout or its docker directory")
    # Resolve the selected existing project from its containers, not from names
    # of unrelated containers or the presence of a Compose file alone.
    base = ["docker", "compose", "--project-directory", str(directory)]
    ids = run(base + ["ps", "--all", "--quiet", "hub"], text=True).split()
    if len(ids) != 1:
        raise RuntimeError("Cannot identify one existing Compose hub in this directory")
    hub = json.loads(run(["docker", "inspect", ids[0]], text=True))[0]
    labels = hub["Config"].get("Labels") or {}
    project = labels.get("com.docker.compose.project")
    actual_files = labels.get("com.docker.compose.project.config_files", "").split(",")
    if not project or not all(Path(p).is_absolute() and Path(p).is_file() for p in actual_files):
        raise RuntimeError("Cannot resolve the existing Compose configuration files")
    args = ["docker", "compose", "--project-directory", str(directory), "--project-name", project]
    for file in actual_files:
        args += ["--file", file]
    resolved = json.loads(run(args + ["config", "--format", "json"], text=True))
    env = resolved["services"]["hub"].get("environment", {})
    public = env.get("PUBLIC_URL")
    if not public or not public.startswith("https://"):
        raise RuntimeError("Existing Compose hub has no usable PUBLIC_URL")
    return {"mode": "compose", "public_url": public, "compose_dir": str(directory.resolve()),
            "compose_project": project, "compose_files": actual_files,
            "compose_override": str(CONFIG.parent / "compose.override.json")}, resolved


def install_code():
    protected_directory(CODE.parent, 0o755)
    current = Path(sys.argv[0]).resolve()
    if zipfile.is_zipfile(current):
        if current != CODE:
            write_file(CODE, current.read_bytes(), 0o755)
    else:
        # Source-checkout entry point: package just the updater, not repo data.
        with tempfile.TemporaryDirectory(prefix="bloxos-updater-code-") as directory:
            package = Path(directory) / "updater"
            package.mkdir()
            for source in Path(__file__).parent.glob("*.py"):
                shutil.copyfile(source, package / source.name)
            archive = Path(directory).with_name(Path(directory).name + ".pyz")
            try:
                zipapp.create_archive(directory, archive, interpreter="/usr/bin/python3", main="updater.cli:entrypoint")
                write_file(CODE, archive.read_bytes(), 0o755)
            finally:
                archive.unlink(missing_ok=True)
    write_file("/usr/local/bin/bloxos-update", "#!/bin/sh\nexec /usr/bin/python3 /usr/local/lib/bloxos-updater/bloxos-update \"$@\"\n", 0o755)


def install_units(mailbox):
    write_file("/etc/systemd/system/bloxos-updater.service", """[Unit]
Description=BloxOS verified host updater
After=network-online.target docker.service
Wants=network-online.target
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 /usr/local/lib/bloxos-updater/bloxos-update worker
TimeoutStartSec=30min
UMask=0077
[Install]
WantedBy=multi-user.target
""")
    write_file("/etc/systemd/system/bloxos-updater.path", f"""[Unit]
Description=Watch for BloxOS update requests
[Path]
PathExists={mailbox}/inbox/request.json
Unit=bloxos-updater.service
[Install]
WantedBy=multi-user.target
""")
    run(["systemctl", "daemon-reload"])
    run(["systemctl", "enable", "bloxos-updater.service"])
    run(["systemctl", "enable", "--now", "bloxos-updater.path"])


def initialize(args):
    pending = CONFIG.parent / "setup.pending.json"
    if pending.exists():
        protected_directory(CONFIG.parent)
        if pending.is_symlink():
            raise RuntimeError("Refusing symlinked setup state")
        config = json.loads(pending.read_text())
        atomic_json(CONFIG, config)
        finish_setup(config)
        return
    if CONFIG.exists():
        raise RuntimeError("Updater is already configured. Use 'bloxos-update update'; do not overwrite deployment identity")
    if sys.platform != "linux" or not shutil.which("systemctl"):
        raise RuntimeError("Host updates currently support Linux with systemd")
    native = systemd_active("bloxos-hub.service") or systemd_active("bloxos-dashboard.service")
    resolved = None
    if args.mode == "native" or (not args.mode and native):
        from .native import discover_native
        config = discover_native(str(Path(args.directory).resolve()))
        if config.get("ambiguous") or not config.get("mode"):
            raise RuntimeError("Cannot prove the existing native deployment; setup made no changes")
    else:
        config, resolved = discover_compose(args.directory)
    if args.public_url:
        if config.get("public_url") and config["public_url"].rstrip("/") != args.public_url.rstrip("/"):
            raise RuntimeError("Supplied public URL disagrees with the running installation")
        config["public_url"] = args.public_url
    if args.ca_file:
        config["ca_file"] = str(Path(args.ca_file).resolve(strict=True))
    config.update(mailbox_dir=str(ROOT / "mailbox"), state_dir=str(ROOT / "state"))
    # Validate the adapter against actual running service/container ownership
    # before writing configuration or changing a service.
    with tempfile.TemporaryDirectory(prefix="bloxos-updater-inspect-") as directory:
        adapter = make_adapter(config, directory)
        adapter.preflight()
        original_images = adapter.saved["images"] if config["mode"] == "compose" else {}
    print(f"Existing installation: {config['mode']} at {config['public_url']}")
    print("Setup preserves its database and keys, installs the host worker, and briefly restarts the hub to enable the update button.")
    if not args.yes and input("Continue with this installation? [y/N] ").strip().lower() not in ("y", "yes"):
        raise RuntimeError("Setup cancelled; no changes made")
    protected_directory(CONFIG.parent)
    protected_directory(ROOT, 0o755)
    protected_directory(ROOT / "state")
    protected_directory(ROOT / "mailbox", 0o755)
    protected_directory(ROOT / "mailbox" / "outbox", 0o755)
    try:
        gid = grp.getgrnam("bloxos-updater").gr_gid
    except KeyError:
        run(["groupadd", "--system", "bloxos-updater"])
        gid = grp.getgrnam("bloxos-updater").gr_gid
    inbox = ROOT / "mailbox" / "inbox"
    if inbox.exists() and not inbox.is_symlink() and inbox.stat().st_uid == 0 and inbox.stat().st_gid == gid:
        os.chmod(inbox, 0o700)
    protected_directory(inbox)
    os.chown(inbox, 0, gid)
    os.chmod(inbox, 0o770)
    install_code()
    if config["mode"] == "compose":
        # Freeze the rendered deployment config root-private. A writable source
        # checkout must not become privileged instructions for future updates.
        base_file = CONFIG.parent / "compose.base.json"
        atomic_json(base_file, resolved)
        config["compose_files"] = [str(base_file)]
        override = {"services": {"hub": {
            "image": original_images["hub"],
            "environment": {"BLOXOS_UPDATER_DIR": "/run/bloxos-updater"},
            "group_add": [str(gid)],
            "volumes": [str(ROOT / "mailbox") + ":/run/bloxos-updater"],
        }, "dashboard": {"image": original_images["dashboard"]}}}
        atomic_json(config["compose_override"], override)
        # Obtain the actual selected Caddy root from the local container,
        # never a trust-all download from the public URL.
        ca = make_adapter(config, str(ROOT / "state")).local_ca()
        if ca:
            ca_path = CONFIG.parent / "public-ca.crt"
            write_file(ca_path, ca, 0o644)
            config["ca_file"] = str(ca_path)
    else:
        dropin = Path("/etc/systemd/system") / (config["hub_unit"] + ".d") / "90-bloxos-updater.conf"
        write_file(dropin, f"[Service]\nSupplementaryGroups=bloxos-updater\nEnvironment=BLOXOS_UPDATER_DIR={ROOT}/mailbox\n")
    # A failed service restart can be retried without rediscovering or changing
    # the selected installation. Do not advertise availability until it works.
    atomic_json(pending, config)
    atomic_json(CONFIG, config)
    finish_setup(config)


def finish_setup(config):
    mailbox = Mailbox(config["mailbox_dir"])
    install_units(config["mailbox_dir"])
    if config["mode"] == "compose":
        run(make_adapter(config, str(ROOT / "state")).command("up", "-d", "--no-deps", "--no-build", "--pull", "never", "hub"))
    else:
        run(["systemctl", "restart", config["hub_unit"]])
    mailbox.write_status("idle", message="Updater configured")
    mailbox.write_capabilities(config["mode"])
    (CONFIG.parent / "setup.pending.json").unlink(missing_ok=True)
    print("Setup complete. Update command: sudo bloxos-update update")


def make_adapter(raw, directory):
    if raw["mode"] == "compose":
        return ComposeAdapter(raw, directory)
    from .native import NativeAdapter
    return NativeAdapter(raw, directory)


def worker():
    config = Config.load(str(CONFIG))
    protected_directory(config.state_dir)
    return 0 if run_worker(config) in ("idle", "succeeded") else 1


def request_update():
    config = Config.load(str(CONFIG))
    mailbox = Mailbox(config.mailbox_dir)
    request_id = str(uuid.uuid4())
    body = json.dumps({"request_id": request_id, "target_version": "latest"}).encode()
    with tempfile.NamedTemporaryFile(dir=mailbox.inbox, prefix=".request-", delete=False) as stream:
        temporary = Path(stream.name)
        stream.write(body)
        stream.flush()
        os.fsync(stream.fileno())
    try:
        # PathExists must never wake the worker on a partially written request.
        os.link(temporary, mailbox.request_path, follow_symlinks=False)
    finally:
        temporary.unlink(missing_ok=True)
    run(["systemctl", "start", "--no-block", "bloxos-updater.service"])
    print("Update requested. The host worker continues if this terminal disconnects.")
    previous = None
    for _ in range(1800):
        try:
            status = json.loads(Path(mailbox.status_path).read_text())
        except (OSError, ValueError):
            time.sleep(1)
            continue
        if status.get("request_id") == request_id:
            if status.get("state") != previous:
                previous = status.get("state")
                print(previous + ": " + status.get("message", ""), flush=True)
            if previous in TERMINAL:
                return 0 if previous == "succeeded" else 1
        time.sleep(1)
    raise RuntimeError("Worker has not reported completion. Check: sudo systemctl status bloxos-updater")


def main(argv=None):
    parser = argparse.ArgumentParser(description="Update the existing BloxOS installation with backup and public verification")
    parser.add_argument("command", choices=("init", "update", "worker", "status"))
    parser.add_argument("--directory", default=os.getcwd(), help="existing checkout/Compose directory (one-time setup)")
    parser.add_argument("--mode", choices=("native", "compose"), help="explicit installation type (does not bypass ownership checks)")
    parser.add_argument("--public-url", help="must match the existing deployment")
    parser.add_argument("--ca-file", help="existing trusted public CA file")
    parser.add_argument("--yes", action="store_true", help="accept the displayed setup target without a prompt")
    args = parser.parse_args(argv)
    if os.geteuid() != 0:
        parser.error("run with sudo (the web application never receives these privileges)")
    os.umask(0o077)
    try:
        if args.command == "init" or (args.command == "update" and (not CONFIG.exists() or (CONFIG.parent / "setup.pending.json").exists())):
            initialize(args)
            if args.command == "init":
                return 0
        if args.command == "worker":
            return worker()
        if args.command == "update":
            return request_update()
        config = Config.load(str(CONFIG))
        print(Path(config.mailbox_dir, "outbox", "status.json").read_text())
        return 0
    except (UpdaterError, RuntimeError, OSError, ValueError) as error:
        print(f"BloxOS updater: {error}", file=sys.stderr)
        return 1


def entrypoint():
    # zipapp's generated launcher ignores a function's return value. Preserve
    # failures for shell automation ("update && next-step" must not lie).
    raise SystemExit(main())
