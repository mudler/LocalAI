# SPDX-License-Identifier: MIT
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from unittest.mock import patch
import unittest

spec = importlib.util.spec_from_file_location("recovery", Path(__file__).with_name("dispatch-diagnostics-recovery.py"))
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)


class RecoveryTests(unittest.TestCase):
    def test_manifest_identity(self):
        manifest = Path(__file__).parents[1] / "diagnostics/historical-builder-manifest.json"
        self.assertEqual(len(recovery.manifest(manifest)["layers"]), 3)
        with tempfile.TemporaryDirectory() as directory:
            bad = Path(directory) / "manifest"
            bad.write_bytes(manifest.read_bytes() + b"\n")
            with self.assertRaises(ValueError):
                recovery.manifest(bad)

    def test_stream_integrity_and_atomic_failure(self):
        data = b"small offline fixture"
        descriptor = {"digest": "sha256:" + hashlib.sha256(data).hexdigest(), "size": len(data)}
        for payload in (data, data[:-1], data + b"x", b"X" * len(data)):
            with self.subTest(payload=payload), tempfile.TemporaryDirectory() as directory:
                target = Path(directory) / "blob"
                if payload == data:
                    recovery.store(io.BytesIO(payload), target, descriptor)
                    self.assertEqual(target.read_bytes(), data)
                else:
                    with self.assertRaises(ValueError):
                        recovery.store(io.BytesIO(payload), target, descriptor)
                    self.assertFalse(target.exists())
                    self.assertFalse(target.with_suffix(".part").exists())

    def test_oci_layout_offline(self):
        manifest = Path(__file__).parents[1] / "diagnostics/historical-builder-manifest.json"
        with tempfile.TemporaryDirectory() as directory:
            recovery.layout(manifest, Path(directory))
            index = json.loads((Path(directory) / "index.json").read_text())
            self.assertEqual(index["manifests"][0]["digest"], "sha256:" + recovery.MANIFEST_SHA)
            self.assertEqual(index["manifests"][0]["size"], 864)

    def test_download_uses_only_public_digest_reads(self):
        original = Path(__file__).parents[1] / "diagnostics/historical-builder-manifest.json"
        doc = recovery.manifest(original)
        config = json.dumps({"architecture": "arm64", "os": "linux"}).encode()
        payloads = [config, b"layer one", b"layer two", b"layer three"]
        for descriptor, payload in zip([doc["config"], *doc["layers"]], payloads):
            descriptor.update(digest="sha256:" + hashlib.sha256(payload).hexdigest(), size=len(payload))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            recovery.layout(original, root)
            with patch.object(recovery, "layout", return_value=doc), patch.object(
                recovery.urllib.request, "urlopen", side_effect=[io.BytesIO(p) for p in payloads]
            ) as request:
                recovery.recover(original, root)
            self.assertEqual(request.call_count, 4)
            for call in request.call_args_list:
                self.assertTrue(call.args[0].startswith(recovery.BLOBS + "sha256:"))
                self.assertEqual(call.kwargs, {"timeout": 120})


if __name__ == "__main__":
    unittest.main()
