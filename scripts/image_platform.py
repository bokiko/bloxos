#!/usr/bin/env python3
"""Resolve a multi-platform image index to one platform's immutable child digest.

A thin wrapper so the workflow does not re-implement the resolution. The logic
lives in export-server-bundle.py's platform_image, which already handles the
classic-store collision where `docker pull --platform` can hand back a
different image than the index entry for that platform.
"""
import argparse
import importlib.util
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "export_server_bundle", Path(__file__).resolve().parent / "export-server-bundle.py")
_module = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_module)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--platform", required=True)
    args = parser.parse_args()
    print(_module.platform_image(args.image, args.platform))


if __name__ == "__main__":
    main()
