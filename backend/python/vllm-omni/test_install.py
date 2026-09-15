# SPDX-License-Identifier: MIT
"""CPU-only install and platform regressions: python -m unittest discover -s backend/python/vllm-omni -p 'test_install.py'."""
import ast
import contextlib
import io
import os
from pathlib import Path
import sys
import traceback
import types
import unittest
from unittest.mock import patch

BACKEND = Path(__file__).resolve().parent


class PlatformTests(unittest.TestCase):
    def test_backend_import_uses_supported_platform_api(self):
        tree = ast.parse((BACKEND / 'backend.py').read_text())
        imports = [node for node in tree.body if isinstance(node, ast.ImportFrom)
                   and node.module.startswith('vllm_omni')]
        modules = {}
        platform = types.SimpleNamespace(device_type='cuda', is_npu=lambda: False)
        for node in imports:
            if node.module == 'vllm_omni.utils.platform_utils':
                continue  # Removed upstream; both pinned releases provide platforms instead.
            module = types.ModuleType(node.module)
            for alias in node.names:
                setattr(module, alias.name, platform if alias.name == 'current_platform' else object)
            modules[node.module] = module
        namespace = {}
        with patch.dict('sys.modules', modules):
            exec(compile(ast.Module(body=imports, type_ignores=[]), '<backend imports>', 'exec'), namespace)
        self.assertIs(namespace['current_platform'], platform)

    def load_model(self, model_type, options=None, npu=False):
        tree = ast.parse((BACKEND / 'backend.py').read_text())
        servicer = next(node for node in tree.body if isinstance(node, ast.ClassDef)
                        and node.name == 'BackendServicer')
        method = next(node for node in servicer.body if isinstance(node, ast.FunctionDef)
                      and node.name == 'LoadModel')
        calls = []

        # Both pinned releases accept stage_configs_path and these constructor
        # options. Run the production method, stubbing only its dependencies.
        def omni(model, *, stage_configs_path=None, log_stats=False,
                 stage_init_timeout=300, **kwargs):
            calls.append(dict(model=model, stage_configs_path=stage_configs_path,
                              log_stats=log_stats, stage_init_timeout=stage_init_timeout,
                              **kwargs))
            return object()

        namespace = dict(
            os=os, sys=sys, traceback=traceback,
            torch=types.SimpleNamespace(cuda=types.SimpleNamespace(is_available=lambda: True)),
            current_platform=types.SimpleNamespace(is_npu=lambda: npu),
            parse_options=lambda values: dict(values),
            resolve_model_reference=lambda request: (request.Model, False),
            setup_parsers=lambda options: (None, None),
            DiffusionParallelConfig=lambda **kwargs: kwargs,
            backend_pb2=types.SimpleNamespace(Result=types.SimpleNamespace),
            Omni=omni,
        )
        exec(compile(ast.Module(body=[method], type_ignores=[]), '<LoadModel>', 'exec'), namespace)
        request = types.SimpleNamespace(
            Model='test/model', Type=model_type, Options=list((options or {}).items()),
            EnforceEager=False, GPUMemoryUtilization=0, TensorParallelSize=0,
            TrustRemoteCode=False, MaxModelLen=0,
        )
        instance = types.SimpleNamespace()
        with contextlib.redirect_stderr(io.StringIO()):
            result = namespace['LoadModel'](instance, request, None)
        self.assertTrue(result.success, result.message)
        self.assertEqual(len(calls), 1)
        return calls[0]

    def test_default_tts_load_uses_pinned_constructor(self):
        self.assertIsNone(self.load_model('tts')['stage_configs_path'])

    def test_custom_stage_config_reaches_omni(self):
        self.assertEqual(self.load_model('llm', {'stage_configs_path': '/models/stages.yaml'})
                         ['stage_configs_path'], '/models/stages.yaml')

    def test_diffusion_uses_platform_npu_capability(self):
        for npu in [False, True]:
            with self.subTest(npu=npu):
                kwargs = self.load_model('image', npu=npu)
                self.assertEqual(kwargs['vae_use_slicing'], npu)
                self.assertEqual(kwargs['vae_use_tiling'], npu)


if __name__ == '__main__':
    unittest.main()
