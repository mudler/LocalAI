"""Pins every upstream symbol the backend touches.

TensorFold has no public embedding API and ships several releases a week, so
this is what turns an upstream refactor into a red build on the bump PR rather
than a broken model at runtime. Skipped when TensorFold is not installed (the
unit-test environment); it runs in `make test`, where install.sh has run.
"""
import importlib.util
import inspect
import sys
import unittest

from tf_options import FLAGS

INSTALLED = importlib.util.find_spec("tensorfold") is not None


@unittest.skipUnless(INSTALLED, "tensorfold is not installed")
class EmbedSurfaceTest(unittest.TestCase):
    def test_cli_entry_points_used_by_the_loader(self):
        from tensorfold import cli

        for name in ("build_parser", "cmd_serve", "_config_dir", "_backend"):
            self.assertTrue(callable(getattr(cli, name)), name)

    def test_families_detect_used_by_the_loader(self):
        from tensorfold import families

        self.assertTrue(callable(families.detect))

    def test_capture_points_exist(self):
        from tensorfold.server import http, stacks

        self.assertTrue(callable(http.make_handler))
        self.assertTrue(callable(stacks.start))
        self.assertTrue(callable(stacks.arm))

    @unittest.skipIf(sys.platform == "darwin", "the CUDA server module needs torch")
    def test_cuda_capture_point(self):
        from tensorfold.cuda import server

        self.assertTrue(callable(server.serve))
        params = inspect.signature(server.App.run).parameters
        for name in ("body", "chat", "emit", "prepared", "cancelled"):
            self.assertIn(name, params)
        self.assertIn("body", inspect.signature(server.App.prepare).parameters)

    def test_chat_app_signature(self):
        from tensorfold.server.app import ChatApp

        params = inspect.signature(ChatApp.chat).parameters
        for name in ("messages", "max_tokens", "temperature", "on_delta", "tools",
                     "sampling", "cancellation", "prompt"):
            self.assertIn(name, params)

    def test_request_helpers_used_by_the_mlx_adapter(self):
        from tensorfold.engine import grammar
        from tensorfold.server.cancellation import Cancellation, RequestCancelled
        from tensorfold.server.errors import RequestError
        from tensorfold.server.messages import normalize_messages, validate_modalities
        from tensorfold.server.request_options import parse_numbers, thinking_fields
        from tensorfold.server.tool_policy import ToolCallPolicy
        from tensorfold.server.tools import (
            active_tool_specs,
            parse_tool_calls_from_content,
            tool_choice_requires_call,
        )

        for fn in (normalize_messages, validate_modalities, parse_numbers, thinking_fields,
                   active_tool_specs, parse_tool_calls_from_content, tool_choice_requires_call,
                   grammar.refusal):
            self.assertTrue(callable(fn))
        self.assertTrue(grammar.FIELDS)
        for name in ("delta", "flush", "finish"):
            self.assertTrue(callable(getattr(ToolCallPolicy, name)), name)
        self.assertTrue(issubclass(RequestCancelled, Exception))
        self.assertTrue(issubclass(RequestError, Exception))
        self.assertTrue(callable(Cancellation().cancel))

    def test_every_mapped_option_is_a_real_serve_flag(self):
        from tensorfold import cli

        parser = cli.build_parser()
        for key, (flag, kind) in FLAGS.items():
            if kind == "switch":
                argv = ["serve", "org/model", flag]
            else:
                sample = {"kv_dtype": "int8", "parallel": "2", "drafter": "none", "snapshot_dir": "none",
                          "reasoning_effort": "low"}
                argv = ["serve", "org/model", flag, sample.get(key, "1")]
            try:
                parser.parse_args(argv)
            except SystemExit:
                self.fail(f"option {key} maps to {flag}, which tensorfold serve rejects")

    def test_thinking_and_context_flags_exist(self):
        from tensorfold import cli

        parser = cli.build_parser()
        for flag in (["--thinking"], ["--no-thinking"], ["--context", "0"], ["--no-update-check"]):
            parser.parse_args(["serve", "org/model", *flag])
