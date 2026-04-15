"""
Unit tests for the tinygrad gRPC backend.

These tests cover the cheap paths that don't need a real model checkpoint:
  - Health responds OK
  - Tool-call parsers emit expected ToolCall structures

The full LLM / embeddings / Stable Diffusion / Whisper paths are exercised by
the root-level `make test-extra-backend-tinygrad-all` e2e targets, which boot
the containerized backend against real HF checkpoints.
"""
import os
import subprocess
import sys
import time
import unittest

import grpc

import backend_pb2
import backend_pb2_grpc

sys.path.insert(0, os.path.dirname(__file__))
from tool_parsers.hermes import HermesToolParser  # noqa: E402


class TestHealth(unittest.TestCase):
    def setUp(self):
        self.service = subprocess.Popen(
            ["python3", "backend.py", "--addr", "localhost:50051"]
        )
        time.sleep(5)

    def tearDown(self):
        self.service.kill()
        self.service.wait()

    def test_health(self):
        with grpc.insecure_channel("localhost:50051") as channel:
            stub = backend_pb2_grpc.BackendStub(channel)
            response = stub.Health(backend_pb2.HealthMessage())
            self.assertEqual(response.message, b"OK")


class TestHermesParser(unittest.TestCase):
    def test_single_tool_call(self):
        parser = HermesToolParser()
        text = (
            "Sure, let me check.\n"
            "<tool_call>\n"
            '{"name": "get_weather", "arguments": {"city": "Paris"}}\n'
            "</tool_call>\n"
            "Done."
        )
        content, calls = parser.parse(text)
        self.assertIn("Sure", content)
        self.assertIn("Done", content)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0].name, "get_weather")
        self.assertIn("Paris", calls[0].arguments)

    def test_multi_call_and_thinking(self):
        parser = HermesToolParser()
        text = (
            "<think>I need both.</think>"
            '<tool_call>{"name":"a","arguments":{"x":1}}</tool_call>'
            '<tool_call>{"name":"b","arguments":{}}</tool_call>'
        )
        result = parser.parse_full(text)
        self.assertEqual(result.reasoning, "I need both.")
        self.assertEqual([c.name for c in result.tool_calls], ["a", "b"])
        self.assertEqual(result.tool_calls[0].index, 0)
        self.assertEqual(result.tool_calls[1].index, 1)

    def test_no_tool_call_is_passthrough(self):
        parser = HermesToolParser()
        text = "plain assistant answer with no tool call"
        content, calls = parser.parse(text)
        self.assertEqual(content, text)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
