import os
import tempfile
import unittest
from unittest import mock

from temp_utils import cleanup_paths, materialize_base64


class MaterializeBase64Test(unittest.TestCase):
    def test_removes_materialized_file_after_success(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.object(tempfile, "tempdir", directory):
                with materialize_base64("aGVsbG8=", suffix=".data") as path:
                    with open(path, "rb") as materialized:
                        self.assertEqual(materialized.read(), b"hello")
                self.assertFalse(os.path.exists(path))

    def test_removes_materialized_file_when_consumer_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            with mock.patch.object(tempfile, "tempdir", directory):
                with self.assertRaisesRegex(RuntimeError, "decode failed"):
                    with materialize_base64("aGVsbG8="):
                        raise RuntimeError("decode failed")
                self.assertEqual(os.listdir(directory), [])


class CleanupPathsTest(unittest.TestCase):
    def test_removes_every_registered_path_after_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            paths = [os.path.join(directory, name) for name in ("one.wav", "two.wav")]
            with self.assertRaisesRegex(RuntimeError, "merge failed"):
                with cleanup_paths() as registered:
                    for path in paths:
                        open(path, "wb").close()
                        registered.append(path)
                    raise RuntimeError("merge failed")
            self.assertEqual(os.listdir(directory), [])


if __name__ == "__main__":
    unittest.main()
