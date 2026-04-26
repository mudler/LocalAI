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
from vendor.appsllm_adapter import _hf_to_appsllm_state_dict  # noqa: E402


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


class TestAppsLLMAdapter(unittest.TestCase):
    """Smoke tests for the HF → tinygrad.apps.llm state-dict keymap."""

    def _fake_hf_weights(self, n_layers: int = 2, include_lm_head: bool = True):
        keys = [
            "model.embed_tokens.weight",
            "model.norm.weight",
        ]
        if include_lm_head:
            keys.append("lm_head.weight")
        for l in range(n_layers):
            keys += [
                f"model.layers.{l}.input_layernorm.weight",
                f"model.layers.{l}.post_attention_layernorm.weight",
                f"model.layers.{l}.self_attn.q_proj.weight",
                f"model.layers.{l}.self_attn.k_proj.weight",
                f"model.layers.{l}.self_attn.v_proj.weight",
                f"model.layers.{l}.self_attn.o_proj.weight",
                f"model.layers.{l}.self_attn.q_norm.weight",
                f"model.layers.{l}.self_attn.k_norm.weight",
                f"model.layers.{l}.mlp.gate_proj.weight",
                f"model.layers.{l}.mlp.up_proj.weight",
                f"model.layers.{l}.mlp.down_proj.weight",
            ]
        # sentinel objects so we can verify identity-based aliasing
        return {k: object() for k in keys}

    def test_keymap_renames_every_hf_key(self):
        hf = self._fake_hf_weights(n_layers=2)
        sd = _hf_to_appsllm_state_dict(hf, 2)
        expected = {
            "token_embd.weight", "output_norm.weight", "output.weight",
            "blk.0.attn_norm.weight", "blk.0.ffn_norm.weight",
            "blk.0.attn_q.weight", "blk.0.attn_k.weight", "blk.0.attn_v.weight",
            "blk.0.attn_output.weight",
            "blk.0.attn_q_norm.weight", "blk.0.attn_k_norm.weight",
            "blk.0.ffn_gate.weight", "blk.0.ffn_up.weight", "blk.0.ffn_down.weight",
            "blk.1.attn_norm.weight", "blk.1.ffn_norm.weight",
            "blk.1.attn_q.weight", "blk.1.attn_k.weight", "blk.1.attn_v.weight",
            "blk.1.attn_output.weight",
            "blk.1.attn_q_norm.weight", "blk.1.attn_k_norm.weight",
            "blk.1.ffn_gate.weight", "blk.1.ffn_up.weight", "blk.1.ffn_down.weight",
        }
        self.assertEqual(set(sd.keys()), expected)

    def test_tied_embedding_fallback_when_lm_head_missing(self):
        hf = self._fake_hf_weights(n_layers=1, include_lm_head=False)
        sd = _hf_to_appsllm_state_dict(hf, 1)
        self.assertIn("output.weight", sd)
        self.assertIs(sd["output.weight"], sd["token_embd.weight"])

    def test_unknown_keys_are_skipped(self):
        hf = self._fake_hf_weights(n_layers=1)
        hf["model.layers.0.self_attn.rotary_emb.inv_freq"] = object()
        hf["model.some_unknown.weight"] = object()
        sd = _hf_to_appsllm_state_dict(hf, 1)
        self.assertNotIn("model.some_unknown.weight", sd)
        # Renamed keys still present
        self.assertIn("blk.0.attn_q.weight", sd)

    def test_qkv_bias_models_rejected(self):
        hf = self._fake_hf_weights(n_layers=1)
        hf["model.layers.0.self_attn.q_proj.bias"] = object()
        with self.assertRaises(ValueError) as ctx:
            _hf_to_appsllm_state_dict(hf, 1)
        self.assertIn("Qwen3", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
