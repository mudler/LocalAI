"""Unit tests for the ephemeral PEP 503 index (pypi_mirror_server.py).

Run standalone (Python standard library only, no backend venv needed):
    python3 -m unittest pypi_mirror_server_test
"""

import hashlib
import os
import shutil
import tempfile
import threading
import unittest
import urllib.error
import urllib.request

from pypi_mirror_server import make_server, normalize, resolve_path

WHEEL_BYTES = b"not a real wheel, but the server must serve it verbatim"


class TestHelpers(unittest.TestCase):
    def test_normalize(self):
        self.assertEqual(normalize("Liquid_Audio.Extra"), "liquid-audio-extra")
        self.assertEqual(normalize("torch"), "torch")

    def test_resolve_rejects_traversal(self):
        root = tempfile.mkdtemp()
        try:
            self.assertIsNone(resolve_path(root, "/../etc/passwd"))
            self.assertIsNone(resolve_path(root, "/a/../../etc"))
        finally:
            shutil.rmtree(root)

    def test_resolve_normalized_segment(self):
        root = tempfile.mkdtemp()
        try:
            os.makedirs(os.path.join(root, "jp6", "liquid_audio"))
            found = resolve_path(root, "/jp6/liquid-audio/")
            self.assertEqual(found, os.path.join(root, "jp6", "liquid_audio"))
        finally:
            shutil.rmtree(root)


class TestServer(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = tempfile.mkdtemp()
        project = os.path.join(cls.root, "jp6", "cu129", "torch")
        os.makedirs(project)
        cls.wheel_name = "torch-2.8.0-cp312-cp312-linux_aarch64.whl"
        with open(os.path.join(project, cls.wheel_name), "wb") as f:
            f.write(WHEEL_BYTES)
        cls.server = make_server(cls.root)
        cls.base = "http://127.0.0.1:%d" % cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        shutil.rmtree(cls.root)

    def _get(self, path):
        with urllib.request.urlopen(self.base + path) as resp:
            return resp.status, resp.read()

    def test_project_page_lists_wheel_with_hash(self):
        status, body = self._get("/jp6/cu129/torch/")
        self.assertEqual(status, 200)
        digest = hashlib.sha256(WHEEL_BYTES).hexdigest()
        self.assertIn(
            ('<a href="%s#sha256=%s">' % (self.wheel_name, digest)).encode(), body
        )

    def test_index_listing_names_projects(self):
        status, body = self._get("/jp6/cu129/")
        self.assertEqual(status, 200)
        self.assertIn(b'<a href="torch/">torch</a>', body)

    def test_wheel_download_is_verbatim(self):
        status, body = self._get("/jp6/cu129/torch/" + self.wheel_name)
        self.assertEqual(status, 200)
        self.assertEqual(body, WHEEL_BYTES)

    def test_unknown_project_is_404(self):
        # 404 (not 5xx) matters: resolvers treat it as "not in this index"
        # and fall back to PyPI, which is the whole point of the mirror.
        with self.assertRaises(urllib.error.HTTPError) as ctx:
            self._get("/jp6/cu129/liquid-audio/")
        self.assertEqual(ctx.exception.code, 404)

    def test_directory_without_slash_redirects(self):
        status, _ = self._get("/jp6/cu129/torch")
        # urllib follows the 301; landing on the page proves the redirect
        self.assertEqual(status, 200)


if __name__ == "__main__":
    unittest.main()
