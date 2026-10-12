#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Recover a pinned, public OCI image locally; never contact a registry for writes."""
import hashlib
import json
from pathlib import Path
import sys
import urllib.request

MANIFEST_SHA = "532309008fb34016d855e4bf4408c521bf94fb95e43fd9cdba96a2aa50a8041f"
BLOBS = "https://quay.io/v2/go-skynet/ci-cache/blobs/"


def manifest(path):
    data = path.read_bytes()
    if len(data) != 864 or hashlib.sha256(data).hexdigest() != MANIFEST_SHA:
        raise ValueError("historical manifest identity mismatch")
    return json.loads(data)


def store(source, target, descriptor):
    """Bound memory and bytes written, and expose only fully verified blobs."""
    part = target.with_suffix(".part")
    digest = hashlib.sha256()
    size = 0
    try:
        with part.open("xb") as output:
            while chunk := source.read(1024 * 1024):
                size += len(chunk)
                if size > descriptor["size"]:
                    raise ValueError("blob exceeds declared length")
                digest.update(chunk)
                output.write(chunk)
        if size != descriptor["size"] or "sha256:" + digest.hexdigest() != descriptor["digest"]:
            raise ValueError("blob length or SHA256 mismatch")
        part.replace(target)
    finally:
        part.unlink(missing_ok=True)


def layout(path, root):
    doc = manifest(path)
    blobs = root / "blobs/sha256"
    blobs.mkdir(parents=True, exist_ok=True)
    (blobs / MANIFEST_SHA).write_bytes(path.read_bytes())
    (root / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}\n')
    (root / "index.json").write_text(json.dumps({
        "schemaVersion": 2, "manifests": [{
            "mediaType": doc["mediaType"], "digest": "sha256:" + MANIFEST_SHA,
            "size": 864, "annotations": {"org.opencontainers.image.ref.name": "historical"},
        }],
    }) + "\n")
    return doc


def recover(path, root):
    doc = layout(path, root)
    for descriptor in [doc["config"], *doc["layers"]]:
        target = root / "blobs/sha256" / descriptor["digest"].split(":")[1]
        with urllib.request.urlopen(BLOBS + descriptor["digest"], timeout=120) as response:
            store(response, target, descriptor)
        print(json.dumps({"digest": descriptor["digest"], "size": descriptor["size"], "verified": True}), flush=True)
    config = json.loads((root / "blobs/sha256" / doc["config"]["digest"].split(":")[1]).read_bytes())
    if config["architecture"] != "arm64" or config["os"] != "linux":
        raise ValueError("historical builder is not linux/arm64")


if __name__ == "__main__":
    recover(Path(sys.argv[1]), Path(sys.argv[2]))
