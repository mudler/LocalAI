import ast
import os
import tempfile
import unittest
from types import SimpleNamespace

from model_utils import resolve_model_reference


class ResolveModelReferenceTest(unittest.TestCase):
    def test_existing_model_file_wins(self):
        with tempfile.TemporaryDirectory() as directory:
            request = SimpleNamespace(Model="owner/repo", ModelFile=directory)
            self.assertEqual(resolve_model_reference(request), (directory, True))

    def test_missing_model_file_preserves_legacy_repo_fallback(self):
        request = SimpleNamespace(Model=" owner/repo ", ModelFile="/does/not/exist")
        self.assertEqual(resolve_model_reference(request), ("owner/repo", False))

    def test_empty_request_uses_explicit_default(self):
        request = SimpleNamespace(Model=" ", ModelFile="")
        self.assertEqual(
            resolve_model_reference(request, "default/repo"),
            ("default/repo", False),
        )


class MigratedBackendSourceTest(unittest.TestCase):
    def test_general_backends_use_shared_resolution(self):
        common_dir = os.path.dirname(__file__)
        python_root = os.path.dirname(common_dir)
        for backend in ("transformers", "diffusers", "qwen-asr", "fish-speech"):
            with self.subTest(backend=backend):
                with open(
                    os.path.join(python_root, backend, "backend.py"), encoding="utf-8"
                ) as backend_file:
                    tree = ast.parse(backend_file.read())
                imports = {
                    alias.name
                    for node in ast.walk(tree)
                    if isinstance(node, ast.ImportFrom) and node.module == "model_utils"
                    for alias in node.names
                }
                calls = {
                    node.func.id
                    for node in ast.walk(tree)
                    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
                }
                self.assertIn("resolve_model_reference", imports)
                self.assertIn("resolve_model_reference", calls)


if __name__ == "__main__":
    unittest.main()
