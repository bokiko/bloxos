"""Boot-time recovery gate.

An interrupted update leaves a durable journal. At the next boot the enabled
native app units (or restart:always Compose containers) could otherwise start a
half-installed or rolled-back hub and accept PUBLIC writes before rollback runs
— a lost-write window. `recover_boot` resolves that journal FIRST, under the
worker lock, and reports fail-closed so systemd keeps the public proxy shut
until recovery has durably committed.

It is journal-only: it never reads or applies a pending request (that stays with
the runtime worker). A naive `Before=<app units>` on the worker deadlocks because
the worker synchronously `systemctl start`s those units during rollback; this
gate instead runs recovery in-process and lets systemd bring the proxy up AFTER
the gate via an ordering drop-in.
"""
import os

from . import engine
from .engine import Journal, Transaction, as_config_dict, build_adapter, exclusive_lock


def _native_boot_adapter(config, transaction_dir):
    """Native adapter for the gate: the ONE proxy start that finalize() issues
    becomes a NON-BLOCKING enqueue.

    The proxy unit is ordered `After=` this gate, so a blocking
    `systemctl start <proxy>` inside the gate's own ExecStart would self-wait
    forever. `--no-block` enqueues the start and returns; systemd opens the proxy
    once the gate exits. finalize() is reached only after a durable terminal, so
    restarting the gate after a fixed failure re-issues this enqueue and reopens
    Caddy — a forward `Requires=` never restarts a cancelled proxy job on its
    own. Every other systemctl call (stop, hub/dashboard start, daemon-reload)
    runs REAL: the backends are not gated and their restarts do not deadlock.
    """
    from .native import NativeAdapter, default_runner
    merged = as_config_dict(config)
    proxy_unit = merged.get("proxy_unit")

    def boot_runner(cmd):
        if (proxy_unit and cmd[:2] == ["systemctl", "start"]
                and cmd[-1] == proxy_unit and "--no-block" not in cmd):
            cmd = ["systemctl", "start", "--no-block", proxy_unit]
        return default_runner(cmd)

    # Backend readiness stays REAL: _start_backends probes the LOOPBACK hub
    # /health and dashboard root, and those units are ungated and started
    # synchronously (no deadlock), so recovery must confirm the restored
    # originals actually answer before finalize reopens the proxy. ONLY the
    # proxy start is made non-blocking (it alone is ordered After= this gate).
    return NativeAdapter(merged, transaction_dir, runner=boot_runner)


def recover_boot(config) -> int:
    """Resolve an interrupted transaction under the worker lock; return an exit
    code the recovery gate uses as its fail-closed signal.

    0  — nothing to recover, or recovery reached a durable terminal AND cleared
         the journal (native: finalize enqueued the proxy for systemd to open;
         compose: original restart policies restored). systemd then starts the
         proxy after the gate.
    1  — the journal is retained (a failed rollback) or recovery raised (a
         corrupt/unknown-phase journal, refused before any adapter action). The
         gate fails; the proxy's `Requires=` keeps it closed (native) and the
         maintenance marker stays set (defense in depth), so no public write is
         accepted until an operator retries recovery.
    """
    merged = as_config_dict(config)
    state_dir = merged.get("state_dir")
    if not state_dir:
        # A configured host always has state_dir (Config.load requires it); its
        # absence means we cannot locate the journal, so fail closed.
        return 1
    transaction_dir = os.path.join(state_dir, "transaction")
    os.makedirs(state_dir, exist_ok=True)
    # The lock serializes the gate against any runtime worker. The runtime worker
    # unit is ordered After=+Requires= this gate, so at boot it cannot be holding
    # the lock; the lock here also covers a manual retry racing an on-demand run.
    with exclusive_lock(os.path.join(state_dir, "worker.lock")):
        if not Journal(transaction_dir).exists():
            return 0  # no interrupted transaction
        if merged.get("mode") == "native":
            adapter = _native_boot_adapter(config, transaction_dir)
        else:
            adapter = build_adapter(config, transaction_dir)
        try:
            Transaction(config, adapter).recover()
        except engine.UpdaterError:
            # Corrupt/unknown journal, or a rollback that could not complete:
            # leave the journal and the closed proxy exactly as they are.
            return 1
        # recover() clears the journal ONLY on a durable, finalized terminal.
        return 1 if Journal(transaction_dir).exists() else 0
