#!/usr/bin/env python3
"""Read-only identity acceptance for the disposable Compose smoke stack."""
import json
import os
from pathlib import Path
import ssl
import subprocess
import urllib.request

COMPOSE = ["docker", "compose", "--project-directory",
           str(Path(__file__).resolve().parents[2] / "docker")]


def container_read(service, *command):
    return subprocess.check_output(COMPOSE + ["exec", "-T", service, *command],
                                   timeout=15, text=True)


def main():
    # Trust comes from the selected local container, not an unverified download.
    ca = container_read("caddy", "cat", "/data/caddy/pki/authorities/local/root.crt")
    context = ssl.create_default_context(cadata=ca)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                        urllib.request.HTTPSHandler(context=context))
    base = "https://" + os.environ.get("HUB_HOST", "127.0.0.1")
    for component, port, path in [("hub", 4000, "/api/build-info"),
                                  ("dashboard", 3000, "/build-info")]:
        direct = json.loads(container_read(component, "wget", "-qO-",
                                           f"http://127.0.0.1:{port}{path}"))
        with opener.open(base + path, timeout=10) as response:
            assert response.url == base + path, "unexpected proxy redirect"
            assert "no-store" in response.headers.get("Cache-Control", ""), "identity cached"
            public = json.load(response)
        assert direct == public, f"public route is not serving the selected {component} instance"
        assert public["component"] == component and public["instance_id"], "missing identity"
        for field, variable in [("version", "BLOXOS_EXPECT_VERSION"),
                                ("revision", "BLOXOS_EXPECT_REVISION")]:
            expected = os.environ.get(variable)
            if expected:
                assert public[field] == expected, f"{component} has wrong {field}"
        print(f"PASS: public {component} matches selected container build and process")


if __name__ == "__main__":
    main()
