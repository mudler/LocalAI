"""Unit tests for the mlx/mlx-vlm shared helpers (mlx_utils.py).

Run standalone (Python standard library only, no backend venv needed):
    python3 -m unittest mlx_utils_test

These mirror the server-less helper tests in backend/python/mlx/test.py
(TestSharedHelpers), but live here so they run on any platform: the mlx
test module imports grpc/backend_pb2 at import time and needs the MLX venv,
whereas mlx_utils only needs the standard library.
"""

import types
import unittest

from mlx_utils import parse_tool_calls, split_reasoning


class TestSplitReasoning(unittest.TestCase):
    def test_both_tags(self):
        r, c = split_reasoning(
            "<think>step 1\nstep 2</think>The answer is 42.", "<think>", "</think>"
        )
        self.assertEqual(r, "step 1\nstep 2")
        self.assertEqual(c, "The answer is 42.")

    def test_implicit_opener_only_closing_tag(self):
        # Qwen3.5 opens the assistant turn already inside thinking, so the
        # output carries only the closing tag; everything before it is reasoning.
        r, c = split_reasoning(
            "The user is asking about the weather.\n</think>\n\nThe weather in Rome is sunny.",
            "<think>",
            "</think>",
        )
        self.assertEqual(r, "The user is asking about the weather.")
        self.assertEqual(c, "The weather in Rome is sunny.")

    def test_no_tags_at_all(self):
        r, c = split_reasoning("just text", "<think>", "</think>")
        self.assertEqual(r, "")
        self.assertEqual(c, "just text")

    def test_empty_think_end_and_no_opener_match(self):
        # No think_end to anchor on, and the opener is absent → return unchanged.
        r, c = split_reasoning("no opener here", "<think>", "")
        self.assertEqual(r, "")
        self.assertEqual(c, "no opener here")

    def test_empty_text(self):
        r, c = split_reasoning("", "<think>", "</think>")
        self.assertEqual(r, "")
        self.assertEqual(c, "")


class TestParseToolCalls(unittest.TestCase):
    def test_with_shim(self):
        tm = types.SimpleNamespace(
            tool_call_start="<tool_call>",
            tool_call_end="</tool_call>",
            parse_tool_call=lambda body, tools: {
                "name": "get_weather",
                "arguments": {"location": body.strip()},
            },
        )
        calls, remaining = parse_tool_calls(
            "Sure: <tool_call>Paris</tool_call>", tm, tools=None
        )
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["name"], "get_weather")
        self.assertEqual(calls[0]["arguments"], '{"location": "Paris"}')
        self.assertEqual(calls[0]["index"], 0)
        self.assertNotIn("<tool_call>", remaining)


if __name__ == "__main__":
    unittest.main()
