#!/usr/bin/env python3
"""Read-only upgrade preflight: verify the public route serves the intended build.

Compares build identity reported by the PUBLIC endpoint against the build the
operator expects AND against the instance identity of the locally-running
hub/dashboard (via their direct URLs). Nothing here installs, stops, restarts
or mutates anything; every answer is PASS, FAIL, or UNKNOWN with evidence.

Build-info contract (both endpoints): JSON object with string fields
component, version, revision, instance_id, served with Cache-Control:
no-store. Hub: GET {base}/api/build-info. Dashboard: GET {base}/build-info.
An endpoint without this metadata is a legacy install — UNKNOWN, never PASS.

A verified PASS requires ALL of: valid metadata on hub and dashboard, the
expected build on both, matching identity fields between each public and
local endpoint, and TLS that verifies (system trust or --ca-file; never
insecure). Anything short of that is FAIL or UNKNOWN, never a green result
for an older or unverifiable deployment.

Exit codes: 0 verified PASS, 1 FAIL (wrong build/instance/untrusted), 2 UNKNOWN
(missing metadata, unreachable, invalid response, tooling/permission error).
"""
import argparse
import json
import math
import os
import shutil
import socket
import ssl
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

MAX_BODY = 64 * 1024
COMPONENTS = {"hub": "api/build-info", "dashboard": "build-info"}
FIELDS = ("component", "version", "revision", "instance_id")
LOOPBACK_HOSTS = {"127.0.0.1", "localhost", "::1"}
DEV_MARKERS = {"", "dev", "development", "unknown", "unreleased", "snapshot"}

PASS, FAIL, UNKNOWN = 0, 1, 2
results = []


def record(status, name, detail=""):
    tag = {PASS: "PASS", FAIL: "FAIL", UNKNOWN: "UNKNOWN"}[status]
    results.append(status)
    print(f"{tag}  {name}" + (f" — {detail}" if detail else ""))


def safe(text):
    """Strip terminal control characters from anything printed."""
    return "".join(c for c in str(text) if c.isprintable() and c != "\x7f")


class PreflightError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


def valid_url(raw, allow_http_loopback):
    """Validate the raw base URL BEFORE any path is appended: a base carrying
    userinfo/query/fragment or control characters must not smuggle an
    appended build-info path. https anywhere; http on loopback only (the
    supported development fixture policy)."""
    if any(c.isspace() or not c.isprintable() for c in raw):
        raise PreflightError(UNKNOWN, "URL contains whitespace or control characters")
    try:
        parts = urllib.parse.urlsplit(raw)
        host = parts.hostname  # None when absent
        _ = parts.port  # raises ValueError on an invalid port
    except ValueError as err:
        raise PreflightError(UNKNOWN, f"invalid URL: {type(err).__name__}")
    if not host:
        raise PreflightError(UNKNOWN, "URL has no hostname")
    if parts.username or parts.password or parts.query or parts.fragment:
        raise PreflightError(UNKNOWN, "URL must not contain userinfo, query or fragment")
    if parts.scheme == "https":
        return True
    if parts.scheme == "http" and allow_http_loopback and host in LOOPBACK_HOSTS:
        return True
    raise PreflightError(UNKNOWN, "URL must be https, or http on loopback only")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # treat every redirect as a terminal response


def fetch_json(url, timeout, opener):
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    try:
        with opener.open(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 200)
            headers = resp.headers
            body = resp.read(MAX_BODY + 1)
    except urllib.error.HTTPError as err:
        if err.code in (301, 302, 303, 307, 308):
            raise PreflightError(FAIL, f"redirect refused (HTTP {err.code}; locations are never followed)")
        raise PreflightError(UNKNOWN, f"HTTP {err.code} — legacy endpoint without build metadata is not verifiable")
    except urllib.error.URLError as err:
        reason = getattr(err, "reason", None)
        if isinstance(reason, ssl.SSLError):
            raise PreflightError(FAIL, "TLS verification failed (certificate not trusted; supply --ca-file or fix trust)")
        raise PreflightError(UNKNOWN, f"unreachable: {type(err).__name__}")
    except ssl.SSLError:
        raise PreflightError(FAIL, "TLS verification failed (certificate not trusted; supply --ca-file or fix trust)")
    except (socket.timeout, TimeoutError, OSError) as err:
        raise PreflightError(UNKNOWN, f"unreachable: {type(err).__name__}")
    if status in (301, 302, 303, 307, 308):
        raise PreflightError(FAIL, "redirect refused (no redirect following to untrusted destinations)")
    if len(body) > MAX_BODY:
        raise PreflightError(UNKNOWN, f"response larger than {MAX_BODY} bytes")
    try:
        data = json.loads(body.decode("utf-8", "strict"))
    except (UnicodeDecodeError, ValueError):
        raise PreflightError(UNKNOWN, "non-JSON response — legacy endpoint without build metadata")
    if not isinstance(data, dict):
        raise PreflightError(UNKNOWN, "build-info is not a JSON object")
    for field in FIELDS:
        value = data.get(field)
        if not isinstance(value, str) or not value.strip():
            raise PreflightError(UNKNOWN, f"build-info missing or invalid field {field!r} — legacy endpoint")
    return data, headers


def check_endpoint(name, base_url, want_component, args, opener):
    valid_url(base_url, allow_http_loopback=True)
    info, headers = fetch_json(base_url.rstrip("/") + "/" + COMPONENTS[want_component],
                               args.timeout, opener)
    if info["component"] != want_component:
        raise PreflightError(FAIL, f"wrong backend: component is {safe(info['component'])!r}, expected {want_component!r}")
    if info["version"].strip().lower() in DEV_MARKERS or info["revision"].strip().lower() in DEV_MARKERS:
        raise PreflightError(UNKNOWN, "endpoint reports development/unknown build metadata — not verifiable")
    problems = []
    if args.expect_version and info["version"] != args.expect_version:
        problems.append(f"version {safe(info['version'])} != expected")
    if args.expect_revision and info["revision"] != args.expect_revision:
        problems.append("revision does not match the expected full commit SHA")
    if problems:
        raise PreflightError(FAIL, "wrong build: " + "; ".join(problems))
    if "no-store" not in headers.get("Cache-Control", ""):
        record(UNKNOWN, f"{name} cache header", "build-info served without Cache-Control: no-store")
        raise PreflightError(UNKNOWN, "build-info lacks the no-store contract")
    record(PASS, f"{name} build identity",
           f"version {safe(info['version'])} revision {safe(info['revision'][:12])} instance {safe(info['instance_id'][:12])}")
    return info


def compare_identity(label, public, local, worst):
    mismatches = [f for f in ("version", "revision", "instance_id") if public[f] != local[f]]
    if mismatches:
        record(FAIL, f"{label} identity correlation",
               f"public and local differ on: {', '.join(mismatches)} — public route does not reach the intended instance")
        return max(worst, FAIL)
    record(PASS, f"{label} identity correlation", "public route reaches the intended instance (build + instance match)")
    return worst


def detect_evidence():
    """Read-only deployment evidence. Tooling or permission failures are
    UNKNOWN, never 'absent'. Evidence is informational only and is never used
    to infer the public route — that is decided by identity comparison."""
    lines = []
    try:
        units = sorted(u for u in os.listdir("/etc/systemd/system") if u.startswith("bloxos"))
        lines.append(f"systemd units found: {', '.join(units) if units else 'none'}")
    except OSError as err:
        lines.append(f"systemd units: UNKNOWN ({type(err).__name__})")
    if shutil.which("docker"):
        try:
            proc = subprocess.run(
                ["docker", "ps", "--format", "{{.Names}} {{.Image}}"],
                capture_output=True, text=True, timeout=10)
            if proc.returncode == 0:
                rows = [r for r in proc.stdout.splitlines() if "bloxos" in r]
                lines.append("docker containers mentioning 'bloxos' (NOT proof of a Compose project): "
                             + ("; ".join(rows) if rows else "none"))
            else:
                lines.append(f"docker containers: UNKNOWN (docker ps exit {proc.returncode})")
        except (OSError, subprocess.TimeoutExpired) as err:
            lines.append(f"docker containers: UNKNOWN ({type(err).__name__})")
    else:
        lines.append("docker containers: UNKNOWN (docker CLI not installed — not evidence of absence)")
    return lines


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--public-url", required=True, help="public base URL of the deployment, e.g. https://hub.lan")
    parser.add_argument("--local-hub-url", required=True, help="direct local hub URL, e.g. http://127.0.0.1:4000")
    parser.add_argument("--local-dashboard-url", required=True, help="direct local dashboard URL, e.g. http://127.0.0.1:3000")
    parser.add_argument("--ca-file", help="CA bundle for TLS verification (system trust otherwise; verification is never disabled)")
    parser.add_argument("--expect-version", help="required exact version field, e.g. 1.2.2")
    parser.add_argument("--expect-revision", help="required exact full commit SHA")
    parser.add_argument("--timeout", type=float, default=5.0, help="per-request timeout seconds (default 5, max 60)")
    parser.add_argument("--detect", action="store_true", help="also print read-only deployment evidence")
    args = parser.parse_args()
    if not math.isfinite(args.timeout) or args.timeout <= 0 or args.timeout > 60:
        parser.exit(2, "upgrade-preflight: --timeout must be a finite number within (0, 60]\n")

    context = ssl.create_default_context()
    if args.ca_file:
        try:
            context.load_verify_locations(args.ca_file)
        except (OSError, ssl.SSLError) as err:
            parser.exit(2, f"upgrade-preflight: cannot load --ca-file: {type(err).__name__}\n")
    # Direct connections only: no environment proxies, no redirect following,
    # and the TLS context is explicitly handed to HTTPS requests.
    opener = urllib.request.build_opener(
        NoRedirect,
        urllib.request.HTTPSHandler(context=context),
        urllib.request.ProxyHandler({}),
    )

    worst = PASS
    saw_fail = False
    saw_unknown = False

    def note(status):
        nonlocal saw_fail, saw_unknown
        if status == FAIL:
            saw_fail = True
        elif status == UNKNOWN:
            saw_unknown = True

    def run(status_fn):
        nonlocal worst
        try:
            return status_fn()
        except PreflightError as err:
            record(err.status, "check", safe(err))
            note(err.status)
            return None

    public_hub = run(lambda: check_endpoint("public hub", args.public_url, "hub", args, opener))
    public_dash = run(lambda: check_endpoint("public dashboard", args.public_url, "dashboard", args, opener))
    local_hub = run(lambda: check_endpoint("local hub", args.local_hub_url, "hub", args, opener))
    local_dash = run(lambda: check_endpoint("local dashboard", args.local_dashboard_url, "dashboard", args, opener))

    for label, public, local in (("hub", public_hub, local_hub), ("dashboard", public_dash, local_dash)):
        if public is not None and local is not None:
            worst = compare_identity(label, public, local, worst)
            note(worst)

    if args.detect:
        for line in detect_evidence():
            print(f"evidence  {safe(line)}")

    # Precedence is explicit, not numeric: a decisive FAIL (wrong build, wrong
    # backend, untrusted TLS, refused redirect) always outranks UNKNOWN; a
    # merely unverifiable answer must never mask it.
    print(f"result  {'FAIL' if saw_fail else 'UNKNOWN' if saw_unknown else 'PASS'}")
    return FAIL if saw_fail else UNKNOWN if saw_unknown else PASS


if __name__ == "__main__":
    sys.exit(main())
