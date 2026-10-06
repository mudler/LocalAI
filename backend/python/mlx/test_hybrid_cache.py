# SPDX-License-Identifier: MIT
"""Exercise the real MLX RPC paths without requiring MLX or a model download."""
import asyncio
import importlib.util
import json
from pathlib import Path
import sys
import types
import unittest
from unittest.mock import patch

from mlx_cache import ThreadSafeLRUPromptCache


class Cache:
    def __init__(self):
        self.state = []


class Array:
    def __init__(self, tokens):
        self.tokens = list(tokens)

    def __getitem__(self, key):
        return self.tokens


def load_backend():
    modules = {
        "backend_pb2": types.SimpleNamespace(Reply=types.SimpleNamespace, ChatDelta=types.SimpleNamespace),
        "backend_pb2_grpc": types.SimpleNamespace(BackendServicer=object),
        "grpc": types.SimpleNamespace(StatusCode=types.SimpleNamespace(INTERNAL="internal")),
        "grpc_auth": types.SimpleNamespace(get_auth_interceptors=lambda: []),
        "python_utils": types.SimpleNamespace(messages_to_dicts=lambda messages: messages, parse_options=lambda x: {}),
        "mlx_utils": types.SimpleNamespace(parse_tool_calls=None, split_reasoning=None),
        "mlx_lm": types.SimpleNamespace(load=None, stream_generate=None),
        "mlx_lm.sample_utils": types.SimpleNamespace(make_logits_processors=lambda **kw: None, make_sampler=lambda **kw: None),
        "mlx_lm.models.cache": types.SimpleNamespace(make_prompt_cache=lambda *args: [Cache()], can_trim_prompt_cache=lambda cache: False, trim_prompt_cache=None),
        "mlx": types.ModuleType("mlx"),
        "mlx.core": types.SimpleNamespace(array=Array, eval=lambda *args: None),
    }
    spec = importlib.util.spec_from_file_location("hybrid_cache_backend", Path(__file__).with_name("backend.py"))
    module = importlib.util.module_from_spec(spec)
    with patch.dict(sys.modules, modules):
        spec.loader.exec_module(module)
    return module


backend = load_backend()


class Tokenizer:
    chat_template = "test"

    def apply_chat_template(self, messages, **kwargs):
        if messages[-1]["content"] == "" and getattr(self, "reject_empty", False):
            raise ValueError("empty user not supported")
        prefix = json.dumps(kwargs, sort_keys=True)
        return prefix + "".join(f'<{m["role"]}>{m["content"]}</end>' for m in messages) + "<assistant>"

    def encode(self, text):
        # Pair tokens deliberately straddle message and content boundaries.
        return [text[i:i + 2] for i in range(0, len(text), 2)]


class TestHybridCache(unittest.TestCase):
    def setUp(self):
        self.service = backend.BackendServicer()
        self.service.tokenizer = Tokenizer()
        self.service.model_key = "hybrid"
        self.service.max_kv_size = None
        self.service.options = {}
        self.service.lru_cache = ThreadSafeLRUPromptCache(max_size=3, can_trim_fn=lambda cache: False)
        self.model_inputs = []
        self.generation_inputs = []
        self.batch_sizes = []
        self.fail_generation = False
        self.fail_prefill = False
        self.context = types.SimpleNamespace(set_code=lambda code: setattr(self, "error", code), set_details=lambda value: None)
        self.error = None

        def model(tokens, cache):
            self.model_inputs.extend(tokens)
            self.batch_sizes.append(len(tokens))
            cache[0].state.extend(tokens)
            if self.fail_prefill:
                raise RuntimeError("prefill failed")

        self.service.model = model

        def generate(model, tokenizer, prompt, prompt_cache, **kwargs):
            self.assertTrue(prompt, "generation must receive at least one input token")
            self.generation_inputs.append(list(prompt_cache[0].state) + list(prompt))
            model(prompt, cache=prompt_cache)
            if self.fail_generation:
                raise RuntimeError("generation failed")
            yield types.SimpleNamespace(text="ok", token="OUTPUT", prompt_tokens=len(prompt), generation_tokens=1)

        self.generator = patch.object(backend, "stream_generate", generate)
        self.generator.start()
        self.addCleanup(self.generator.stop)

    def request(self, user="first", system="long shared system " * 20, tools="[]", thinking="true"):
        return types.SimpleNamespace(Prompt="", UseTokenizerTemplate=True, Messages=[{"role": "system", "content": system}, {"role": "user", "content": user}], Tools=tools, Metadata={"enable_thinking": thinking})

    def run_request(self, request, streaming=False, cancel=False):
        expected = self.service._get_tokens_from_prompt(self.service._prepare_prompt(request))
        self.model_inputs.clear()
        self.replies = []
        self.error = None

        async def run():
            if streaming:
                stream = self.service.PredictStream(request, self.context)
                if cancel:
                    await anext(stream)
                    await stream.aclose()
                else:
                    async for reply in stream:
                        self.replies.append(reply)
            else:
                self.replies.append(await self.service.Predict(request, self.context))

        asyncio.run(run())
        if not self.fail_generation and not self.fail_prefill:
            self.assertIsNone(self.error)
        if self.generation_inputs:
            self.assertEqual(self.generation_inputs[-1], expected)
        self.assert_cache_keys()
        return len(self.model_inputs)

    def assert_cache_keys(self):
        for model, key in self.service.lru_cache._lru:
            entry = self.service.lru_cache._get(model, key)
            self.assertEqual(entry.prompt_cache[0].state, list(key))

    def test_shared_prefix_reused_repeatedly_in_both_rpcs(self):
        for streaming in (False, True):
            with self.subTest(streaming=streaming):
                self.service.lru_cache.clear()
                first = self.run_request(self.request(tools='[ {"name": "weather"} ]'), streaming)
                for user in ("second", "third", "first"):
                    reused = self.run_request(self.request(user, tools='[ {"name": "weather"} ]'), streaming)
                    self.assertLess(reused, first // 2)
                self.assertEqual(len(self.service.lru_cache), 1)

    def test_prompt_usage_includes_cached_and_prefilled_tokens(self):
        for streaming in (False, True):
            self.service.lru_cache.clear()
            for user in ("cold", "warm"):
                request = self.request(user)
                self.run_request(request, streaming)
                expected = len(self.service._get_tokens_from_prompt(self.service._prepare_prompt(request)))
                self.assertEqual(self.replies[-1].prompt_tokens, expected)

    def test_tool_and_thinking_prefixes_are_part_of_key(self):
        self.run_request(self.request())
        for kwargs in ({"tools": '[{"name":"new_tool"}]'}, {"thinking": "false"}, {"system": "another system"}):
            request = self.request(**kwargs)
            self.assertEqual(self.run_request(request), len(self.service._get_tokens_from_prompt(self.service._prepare_prompt(request))))

    def test_other_model_does_not_reuse_checkpoint(self):
        first = self.run_request(self.request())
        self.service.model_key = "other"
        self.assertEqual(self.run_request(self.request()), first)

    def test_zero_capacity_and_raw_short_prompts(self):
        self.service.lru_cache.max_size = 0
        for _ in range(2):
            self.run_request(self.request())
            self.assertEqual(len(self.service.lru_cache), 0)
        for text in ("a", "ab", "abcdef"):
            request = self.request()
            request.Prompt = text
            self.run_request(request)
            self.assertEqual(len(self.service.lru_cache), 0)

    def test_raw_prompts_do_not_create_hybrid_checkpoints(self):
        for text in ("a", "ab", "a raw prompt"):
            request = self.request()
            request.Prompt = text
            self.run_request(request)
            self.assertEqual(len(self.service.lru_cache), 0)

    def test_one_token_template_leaves_input_for_generation(self):
        self.service.tokenizer.apply_chat_template = lambda *args, **kwargs: "x"
        self.run_request(self.request())
        self.assertEqual(self.model_inputs, ["x"])
        self.assertEqual(len(self.service.lru_cache), 0)

    def test_prefill_batches_are_bounded(self):
        self.run_request(self.request(system="a long system " * 400))
        self.assertGreater(len(self.batch_sizes), 2)
        self.assertLessEqual(max(self.batch_sizes), 512)

    def test_trimmable_cache_keeps_existing_fetch_behavior(self):
        request = self.request()
        tokens = self.service._get_tokens_from_prompt(self.service._prepare_prompt(request))
        cache = [Cache()]
        cache[0].state = tokens[:5]
        self.service.lru_cache.insert_cache(self.service.model_key, tokens[:5], cache)
        with patch.object(backend, "can_trim_prompt_cache", return_value=True):
            result, remaining, trimmable = self.service._prepare_generation_cache(request, tokens)
        self.assertTrue(trimmable)
        self.assertIs(result, cache)
        self.assertEqual(remaining, tokens[5:])
        self.assertEqual(len(self.service.lru_cache), 0)
        self.assertEqual(self.model_inputs, [])

    def test_unsupported_checkpoint_template_still_generates(self):
        self.service.tokenizer.reject_empty = True
        self.run_request(self.request())
        self.assertIsNone(self.error)
        self.assertEqual(len(self.service.lru_cache), 0)

    def test_errors_do_not_store_partial_generation(self):
        for streaming in (False, True):
            self.service.lru_cache.clear()
            self.fail_generation = True
            self.run_request(self.request(), streaming)
            self.assertEqual(self.error, "internal")
            self.fail_generation = False
            self.run_request(self.request("retry"), streaming)

    def test_prefill_failure_does_not_store_partial_checkpoint(self):
        self.fail_prefill = True
        self.run_request(self.request())
        self.assertEqual(len(self.service.lru_cache), 0)

    def test_cancelled_stream_preserves_only_valid_checkpoint(self):
        first = self.run_request(self.request(), streaming=True, cancel=True)
        reused = self.run_request(self.request("after cancellation"), streaming=True)
        self.assertLess(reused, first // 2)


if __name__ == "__main__":
    unittest.main()
