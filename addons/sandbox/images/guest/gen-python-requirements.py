#!/usr/bin/env python3
"""Rewrite the hashes in python-requirements.txt.

Usage: gen-python-requirements.py python-requirements.txt

Keep the "name==version" lines you want (edit a version to bump it, or paste
the output of "uv pip compile python-requirements.in --python-version 3.13
--python-platform x86_64-manylinux_2_28 --no-header --no-annotate" to change
the set), then run this script. It asks the PyPI JSON API for the files of each
version and keeps the sha256 of the wheels that Debian trixie (Python 3.13,
glibc, x86_64 or aarch64) can install. It never lists a source archive, and
the image installs with --only-binary.
"""
import json
import re
import sys
import urllib.request

from packaging.utils import parse_wheel_filename


def installable(filename):
    _, _, _, tags = parse_wheel_filename(filename)
    for t in tags:
        pure = t.platform == "any" and t.abi == "none" and t.interpreter in ("py3", "py2.py3", "cp313")
        abi3 = re.fullmatch(r"cp3(\d+)", t.interpreter) and t.abi == "abi3" and int(t.interpreter[3:]) <= 13
        native = (t.interpreter in ("py3", "cp313") or abi3) and t.abi in ("cp313", "abi3", "none")
        if pure or (native and "manylinux" in t.platform and re.search(r"(x86_64|aarch64)", t.platform)):
            return True
    return False


def main(path):
    pins = []
    for line in open(path):
        m = re.match(r"^([A-Za-z0-9._-]+)==([^\s\\]+)", line)
        if m:
            pins.append(m.groups())
    out = [
        "# Pinned with hashes for the guest image: Debian trixie, Python 3.13, x86_64 and aarch64.",
        "# Wheels only. Edit a version, then run: python3 gen-python-requirements.py python-requirements.txt",
        "# The top-level packages are in python-requirements.in.",
    ]
    for name, version in pins:
        with urllib.request.urlopen(f"https://pypi.org/pypi/{name}/{version}/json", timeout=30) as r:
            files = json.load(r)["urls"]
        hashes = sorted({f["digests"]["sha256"] for f in files if f["filename"].endswith(".whl") and installable(f["filename"])})
        if not hashes:
            sys.exit(f"no installable wheel for {name}=={version}")
        out.append(f"{name}=={version} \\")
        out.append(" \\\n".join(f"    --hash=sha256:{h}" for h in hashes))
    with open(path, "w") as f:
        f.write("\n".join(out) + "\n")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
