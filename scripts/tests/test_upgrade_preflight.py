#!/usr/bin/env python3
"""Regression tests for scripts/upgrade_preflight.py — local fixtures only.

Every test stands up its own in-process HTTP(S) servers; nothing touches a
real deployment, network path, Docker daemon or systemd.
"""
import contextlib
import http.server
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE.parent / "upgrade_preflight.py"

PASS, FAIL, UNKNOWN = 0, 1, 2

UNREACHABLE_FIXTURE = "unreachable-fixture"
LOCAL_ARGS = ("--local-hub-url", UNREACHABLE_FIXTURE, "--local-dashboard-url", UNREACHABLE_FIXTURE)

HUB_INFO = {"component": "hub", "version": "1.2.2", "revision": "a" * 40, "instance_id": "inst-hub-1"}
DASH_INFO = {"component": "dashboard", "version": "1.2.2", "revision": "a" * 40, "instance_id": "inst-dash-1"}


def json_response(handler, payload, status=200, cache=True):
    body = json.dumps(payload).encode()
    handler.send_response(status)
    handler.send_header("Content-Type", "application/json")
    if cache:
        handler.send_header("Cache-Control", "no-store")
    handler.send_header("Content-Length", str(len(body)))
    handler.end_headers()
    handler.wfile.write(body)


def make_handler(routes):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            route = routes.get(self.path)
            if route is None:
                self.send_response(404)
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(b"<html><body>not found</body></html>")
                return
            kind, payload = route
            if kind == "json":
                json_response(self, payload)
            elif kind == "nocache":
                json_response(self, payload, cache=False)
            elif kind == "html":
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(b"<html><body>old dashboard</body></html>")
            elif kind == "malformed":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b"{not json")
            elif kind == "interrupted":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Transfer-Encoding", "chunked")
                self.end_headers()
                # Announce a 16-byte chunk, then close after just one byte.
                self.wfile.write(b"10\r\n{")
                self.wfile.flush()
                self.close_connection = True
            elif kind == "missing-fields":
                json_response(self, {"component": payload, "version": "1.2.2"})
            elif kind == "nonstring":
                json_response(self, {"component": "hub", "version": "1.2.2",
                                     "revision": "a" * 40, "instance_id": 123})
            elif kind == "redirect":
                self.send_response(302)
                self.send_header("Location", payload)
                self.end_headers()
            elif kind == "oversized":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"component":"hub","padding":"' + b"x" * (128 * 1024) + b'"}')

        def log_message(self, *args):
            pass

    return Handler


@contextlib.contextmanager
def server(routes, tls_cert=None):
    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), make_handler(routes))
    if tls_cert:
        cert, key = tls_cert
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        httpd.socket = context.wrap_socket(httpd.socket, server_side=True)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"{'https' if tls_cert else 'http'}://127.0.0.1:{httpd.server_address[1]}"
    finally:
        httpd.shutdown()
        thread.join(timeout=5)
        httpd.server_close()


def run_cli(*argv):
    # Reserve (but do not listen on) a random port. Never probe a developer's
    # actual BloxOS on 3000/4000, including automatically forwarded VM ports.
    with socket.socket() as reserved:
        reserved.bind(("127.0.0.1", 0))
        unreachable = f"http://127.0.0.1:{reserved.getsockname()[1]}"
        args = [unreachable if value == UNREACHABLE_FIXTURE else value for value in argv]
        return subprocess.run([sys.executable, str(SCRIPT), *args],
                              capture_output=True, text=True, timeout=60)


def write_test_ca(tmpdir):
    """Self-signed test CA + server cert/key generated once via openssl."""
    ca_key = tmpdir / "ca.key"
    ca_crt = tmpdir / "ca.crt"
    srv_key = tmpdir / "srv.key"
    srv_crt = tmpdir / "srv.crt"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                    "-keyout", str(ca_key), "-out", str(ca_crt), "-days", "1",
                    "-subj", "/CN=test-ca",
                    "-addext", "basicConstraints=critical,CA:TRUE",
                    "-addext", "keyUsage=critical,digitalSignature,keyCertSign"],
                   capture_output=True, check=True)
    subprocess.run(["openssl", "req", "-newkey", "rsa:2048", "-nodes",
                    "-keyout", str(srv_key), "-out", str(tmpdir / "srv.csr"),
                    "-subj", "/CN=localhost", "-addext", "subjectAltName=IP:127.0.0.1"],
                   capture_output=True, check=True)
    subprocess.run(["openssl", "x509", "-req", "-in", str(tmpdir / "srv.csr"),
                    "-CA", str(ca_crt), "-CAkey", str(ca_key), "-CAcreateserial",
                    "-out", str(srv_crt), "-days", "1",
                    "-extfile", "/dev/stdin"],
                   input=b"subjectAltName=IP:127.0.0.1", capture_output=True, check=True)
    return str(ca_crt), (str(srv_crt), str(srv_key))


class PreflightTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        try:
            cls.ca_file, cls.server_cert = write_test_ca(Path(cls.tmp.name))
        except Exception as err:
            cls.ca_file, cls.server_cert = None, None
            print(f"note: TLS fixtures unavailable ({err})")

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def routes(self, hub=HUB_INFO, dash=DASH_INFO):
        return {"/api/build-info": ("json", hub), "/build-info": ("json", dash)}

    def test_verified_match_public_and_local(self):
        with server(self.routes()) as base:
            res = run_cli("--public-url", base, "--local-hub-url", base,
                          "--local-dashboard-url", base,
                          "--expect-version", "1.2.2")
            self.assertEqual(res.returncode, PASS, res.stderr + res.stdout)
            self.assertIn("identity correlation", res.stdout)

    def test_wrong_public_backend_fails(self):
        wrong_hub = dict(HUB_INFO, instance_id="inst-OTHER")
        with server(self.routes()) as local, server(self.routes(hub=wrong_hub)) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local)
            self.assertEqual(res.returncode, FAIL, res.stdout)
            self.assertIn("instance_id", res.stdout)

    def test_same_build_different_instance_fails(self):
        other_hub = dict(HUB_INFO, instance_id="inst-hub-2")
        with server(self.routes()) as local, server(self.routes(hub=other_hub)) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local)
            self.assertEqual(res.returncode, FAIL, res.stdout)

    def test_local_public_version_divergence_fails(self):
        newer_hub = dict(HUB_INFO, version="1.2.3", revision="b" * 40)
        with server(self.routes()) as local, server(self.routes(hub=newer_hub)) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local)
            self.assertEqual(res.returncode, FAIL, res.stdout)
            self.assertIn("version", res.stdout)

    def test_missing_metadata_is_unknown_not_success(self):
        with server({}) as base:  # everything 404s
            res = run_cli("--public-url", base, *LOCAL_ARGS)
            self.assertEqual(res.returncode, UNKNOWN, res.stdout)
            self.assertIn("legacy endpoint", res.stdout)

    def test_locals_required_for_verified_result(self):
        with server(self.routes()) as base:
            res = subprocess.run([sys.executable, str(SCRIPT), "--public-url", base],
                                 capture_output=True, text=True, timeout=60)
            self.assertNotEqual(res.returncode, 0, "public-only must not be a verified PASS")

    def test_interrupted_response_is_unknown_without_traceback(self):
        routes = self.routes()
        routes["/api/build-info"] = ("interrupted", None)
        with server(self.routes()) as local, server(routes) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local)
            self.assertEqual(res.returncode, UNKNOWN, res.stdout + res.stderr)
            self.assertIn("interrupted HTTP response", res.stdout)
            self.assertIn("result  UNKNOWN", res.stdout)
            self.assertNotIn("Traceback", res.stderr)

    def test_dashboard_build_mismatch_fails(self):
        old_dash = dict(DASH_INFO, version="1.2.0")
        with server(self.routes()) as local, server(self.routes(dash=old_dash)) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local, "--expect-version", "1.2.2")
            self.assertEqual(res.returncode, FAIL, res.stdout)
            self.assertIn("wrong build", res.stdout)

    def test_dev_metadata_never_passes(self):
        for marker in ("dev", "development", "unknown", "unreleased", "snapshot"):
            dev_hub = dict(HUB_INFO, version=marker)
            with server(self.routes(hub=dev_hub)) as base:
                res = run_cli("--public-url", base, *LOCAL_ARGS)
                self.assertEqual(res.returncode, UNKNOWN, f"{marker}: {res.stdout}")
                self.assertIn("development", res.stdout)

    def test_missing_no_store_is_unknown(self):
        routes = {"/api/build-info": ("nocache", HUB_INFO), "/build-info": ("json", DASH_INFO)}
        with server(routes) as base:
            res = run_cli("--public-url", base, *LOCAL_ARGS)
            self.assertEqual(res.returncode, UNKNOWN, res.stdout)
            self.assertIn("no-store", res.stdout)

    def test_invalid_and_malformed_responses_unknown(self):
        cases = [
            {"/api/build-info": ("malformed", None)},
            {"/api/build-info": ("html", None)},
            {"/api/build-info": ("missing-fields", "hub")},
            {"/api/build-info": ("nonstring", None)},
        ]
        for routes in cases:
            routes["/build-info"] = ("json", DASH_INFO)
            with server(routes) as base:
                res = run_cli("--public-url", base, *LOCAL_ARGS)
                self.assertEqual(res.returncode, UNKNOWN, f"{routes['/api/build-info']}: {res.stdout}")

    def test_redirect_never_followed(self):
        routes = {"/api/build-info": ("redirect", "http://evil.example/x\x1b[31m"),
                  "/build-info": ("json", DASH_INFO)}
        with server(self.routes()) as local, server(routes) as public:
            res = run_cli("--public-url", public, "--local-hub-url", local,
                          "--local-dashboard-url", local)
            self.assertEqual(res.returncode, FAIL, res.stdout)
            self.assertIn("redirect refused", res.stdout)
            self.assertNotIn("evil.example", res.stdout)  # never echo redirect targets

    def test_decisive_fail_outranks_unknown(self):
        # Wrong build on public dashboard while local hub is unreachable:
        # a known-wrong answer must not be reported merely UNKNOWN.
        old_dash = dict(DASH_INFO, version="1.2.0")
        with server(self.routes(dash=old_dash)) as public:
            res = run_cli("--public-url", public, "--local-hub-url", "http://127.0.0.1:9",
                          "--local-dashboard-url", public, "--expect-version", "1.2.2")
            self.assertEqual(res.returncode, FAIL, res.stdout)

    def test_url_validation_rejects_userinfo_query_fragment_and_remote_http(self):
        for bad in ("https://user:pw@h.example", "https://h.example/?x=1",
                    "https://h.example/#frag", "http://10.0.0.5"):
            res = run_cli("--public-url", bad, *LOCAL_ARGS)
            self.assertNotEqual(res.returncode, 0, bad)

    def test_url_validation_edge_cases(self):
        for bad, want in (
            ("https://h.example/a b", "whitespace"),
            ("https://h.example/a\x1fb", "control"),
            ("https://", "hostname"),
            ("http://[::1", "invalid URL"),
            ("https://h.example:abc", "invalid URL"),
            ("https://h.example/base?sneaky=1", "query"),
        ):
            res = run_cli("--public-url", bad, *LOCAL_ARGS)
            self.assertNotEqual(res.returncode, 0, bad)

    def test_public_loopback_http_allowed(self):
        # Development fixture policy: http on loopback is accepted for public.
        with server(self.routes()) as base:  # plain http on 127.0.0.1
            res = run_cli("--public-url", base, "--local-hub-url", base,
                          "--local-dashboard-url", base, "--expect-version", "1.2.2")
            self.assertEqual(res.returncode, PASS, res.stdout)

    def test_timeout_must_be_finite(self):
        res = run_cli("--public-url", "https://h.example", *LOCAL_ARGS, "--timeout", "nan")
        self.assertEqual(res.returncode, 2, res.stdout)

    def test_oversized_response_unknown(self):
        routes = {"/api/build-info": ("oversized", None), "/build-info": ("json", DASH_INFO)}
        with server(routes) as base:
            res = run_cli("--public-url", base, *LOCAL_ARGS)
            self.assertEqual(res.returncode, UNKNOWN, res.stdout)

    def test_unreachable_unknown(self):
        res = run_cli("--public-url", "http://127.0.0.1:9", *LOCAL_ARGS, "--timeout", "2")
        self.assertEqual(res.returncode, UNKNOWN, res.stdout)

    def test_tls_with_ca_file_and_never_insecure(self):
        if not self.ca_file:
            self.skipTest("openssl fixtures unavailable")
        with server(self.routes(), tls_cert=self.server_cert) as base:
            res = run_cli("--public-url", base, "--local-hub-url", base,
                          "--local-dashboard-url", base, "--ca-file", self.ca_file)
            self.assertEqual(res.returncode, PASS, res.stdout)
            res2 = run_cli("--public-url", base, "--local-hub-url", base,
                           "--local-dashboard-url", base)  # self-signed: must fail, never auto-trust
            self.assertEqual(res2.returncode, FAIL, res2.stdout)
            self.assertIn("TLS verification failed", res2.stdout)


if __name__ == "__main__":
    unittest.main()
