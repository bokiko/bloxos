"""Exercise the verifier with real HTTPS, not just mocked request handlers."""
import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPT = Path(__file__).resolve().parents[1] / "upgrade_preflight.py"


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        component = "hub" if self.path == "/api/build-info" else "dashboard"
        body = json.dumps({"component": component, "version": "v1.2.3",
                           "revision": "a" * 40,
                           "instance_id": component + "-" + self.server.identity}).encode()
        self.send_response(200)
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


@unittest.skipUnless(shutil.which("openssl"), "openssl required for isolated TLS fixture")
class RealTLS(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="bloxos-upgrade-tls-")
        cls.cert = Path(cls.temp.name) / "cert.pem"
        key = Path(cls.temp.name) / "key.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                        "-keyout", str(key), "-out", str(cls.cert), "-days", "1",
                        "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost"],
                       check=True, capture_output=True, timeout=20)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cls.cert, key)
        cls.servers = []
        cls.threads = []
        for identity in ["selected", "wrong"]:
            server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
            server.identity = identity
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            cls.servers.append(server)
            cls.threads.append(thread)

    @classmethod
    def tearDownClass(cls):
        for server, thread in zip(cls.servers, cls.threads):
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
        cls.temp.cleanup()

    def run_check(self, *, trusted=True, wrong=False, env=None):
        selected = f"https://localhost:{self.servers[0].server_port}"
        public = f"https://localhost:{self.servers[int(wrong)].server_port}"
        args = [os.sys.executable, str(SCRIPT), "--public-url", public,
                "--local-hub-url", selected, "--local-dashboard-url", selected,
                "--expect-version", "v1.2.3", "--expect-revision", "a" * 40]
        if trusted:
            args += ["--ca-file", str(self.cert)]
        return subprocess.run(args, capture_output=True, text=True, timeout=15, env=env)

    def test_private_ca_is_actually_used(self):
        result = self.run_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_untrusted_tls_never_passes(self):
        self.assertNotEqual(self.run_check(trusted=False).returncode, 0)

    def test_same_build_wrong_instance_rejected(self):
        self.assertNotEqual(self.run_check(wrong=True).returncode, 0)

    def test_environment_proxy_cannot_redirect_local_comparison(self):
        env = dict(os.environ, HTTPS_PROXY="http://127.0.0.1:1", https_proxy="http://127.0.0.1:1",
                   HTTP_PROXY="http://127.0.0.1:1", http_proxy="http://127.0.0.1:1",
                   NO_PROXY="", no_proxy="")
        result = self.run_check(env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
