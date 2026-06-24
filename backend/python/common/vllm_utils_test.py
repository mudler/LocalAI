"""Unit tests for the shared vLLM backend helpers (vllm_utils.py).

Run standalone (Python standard library only, no backend venv needed):
    cd backend/python/common && python3 -m unittest vllm_utils_test

``vllm_utils`` imports vLLM lazily (inside functions), so the module is
importable without the vLLM wheel. ``AsyncEngineArgs`` is stood in for by a
local dataclass that mirrors the field shapes that matter: plain scalars,
``Optional[...]`` scalars (vLLM's tri-state flags) and dict-valued fields.
"""

import contextlib
import dataclasses
import io
import unittest
from typing import Dict, Literal, Optional, Union

from vllm_utils import apply_options_to_engine_args, normalize_option_key


@dataclasses.dataclass
class FakeEngineArgs:
    model: str = ""
    quantization: Optional[str] = None
    kv_cache_dtype: str = "auto"
    enable_prefix_caching: Optional[bool] = None
    enforce_eager: bool = False
    max_model_len: Optional[int] = None
    tensor_parallel_size: int = 1
    gpu_memory_utilization: float = 0.9
    limit_mm_per_prompt: Optional[Dict[str, int]] = None
    # vLLM types dtype as a Literal of strings, several of which contain the
    # word "float" - a naive match reads that as a float field.
    dtype: Literal["auto", "float16", "bfloat16"] = "auto"
    seed: Union[int, str, None] = None


# vLLM's arg_utils is annotated under PEP 563, where dataclasses.fields() hands
# back annotations as plain strings rather than types.
StringAnnotatedEngineArgs = dataclasses.make_dataclass(
    "StringAnnotatedEngineArgs",
    [
        ("max_model_len", "int | None", dataclasses.field(default=None)),
        ("enable_prefix_caching", "Optional[bool]", dataclasses.field(default=None)),
        ("kv_cache_dtype", "str", dataclasses.field(default="auto")),
    ],
)


def _apply(options, **overrides):
    """Apply options to a fresh FakeEngineArgs, returning (result, stderr)."""
    err = io.StringIO()
    with contextlib.redirect_stderr(err):
        out = apply_options_to_engine_args(FakeEngineArgs(**overrides), options)
    return out, err.getvalue()


class TestApplyOptionsToEngineArgs(unittest.TestCase):
    def test_string_option_is_applied(self):
        out, _ = _apply(["--quantization:gptq_marlin"])
        self.assertEqual(out.quantization, "gptq_marlin")

    def test_valueless_flag_enables_boolean_field(self):
        out, _ = _apply(["--enable-prefix-caching"])
        self.assertIs(out.enable_prefix_caching, True)

    def test_boolean_field_accepts_explicit_false(self):
        out, _ = _apply(["--enable-prefix-caching:false"], enable_prefix_caching=True)
        self.assertIs(out.enable_prefix_caching, False)

    def test_integer_field_is_coerced(self):
        out, _ = _apply(["--max-model-len:4096", "--tensor-parallel-size:2"])
        self.assertEqual(out.max_model_len, 4096)
        self.assertEqual(out.tensor_parallel_size, 2)

    def test_float_field_is_coerced(self):
        out, _ = _apply(["--gpu-memory-utilization:0.85"])
        self.assertAlmostEqual(out.gpu_memory_utilization, 0.85)

    def test_equals_separator_is_accepted(self):
        out, _ = _apply(["--kv-cache-dtype=fp8_e5m2"])
        self.assertEqual(out.kv_cache_dtype, "fp8_e5m2")

    def test_dict_field_is_parsed_as_json(self):
        out, _ = _apply(['--limit-mm-per-prompt:{"image": 4}'])
        self.assertEqual(out.limit_mm_per_prompt, {"image": 4})

    def test_non_flag_options_are_left_alone(self):
        out, err = _apply(["tool_parser:hermes", "reasoning_parser:qwen3", "vad_only"])
        self.assertEqual(out, FakeEngineArgs())
        self.assertEqual(err, "")

    def test_unknown_flag_warns_and_is_skipped(self):
        out, err = _apply(["--not-a-real-flag:1", "--quantization:awq"])
        self.assertEqual(out.quantization, "awq")
        self.assertIn("not_a_real_flag", err)
        self.assertIn("unknown", err.lower())

    def test_parser_flags_are_not_reported_as_unknown(self):
        # LocalAI consumes these itself; whether the engine dataclass also has
        # the field depends on the vLLM version, and warning about them would
        # send users chasing a non-problem.
        out, err = _apply(["--tool-parser:hermes", "--reasoning-parser:qwen3"])
        self.assertEqual(out, FakeEngineArgs())
        self.assertEqual(err, "")

    def test_unknown_flag_hints_at_the_closest_field(self):
        _, err = _apply(["--max-model-length:4096"])
        self.assertIn("max_model_len", err)

    def test_uncoercible_value_warns_and_is_skipped(self):
        out, err = _apply(["--max-model-len:lots"])
        self.assertIsNone(out.max_model_len)
        self.assertIn("max_model_len", err)

    def test_valueless_flag_on_non_boolean_field_warns_and_is_skipped(self):
        out, err = _apply(["--quantization"])
        self.assertIsNone(out.quantization)
        self.assertIn("quantization", err)

    def test_empty_options_returns_the_same_engine_args(self):
        original = FakeEngineArgs(model="m")
        self.assertIs(apply_options_to_engine_args(original, []), original)


class TestFieldTypeInference(unittest.TestCase):
    def test_literal_typed_field_keeps_its_string_value(self):
        out, err = _apply(["--dtype:bfloat16"])
        self.assertEqual(out.dtype, "bfloat16")
        self.assertEqual(err.count("skipping"), 0)

    def test_ambiguous_union_falls_back_to_literal_inference(self):
        out, _ = _apply(["--seed:42"])
        self.assertEqual(out.seed, 42)

    def test_string_annotations_are_understood(self):
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            out = apply_options_to_engine_args(
                StringAnnotatedEngineArgs(),
                ["--max-model-len:4096", "--enable-prefix-caching", "--kv-cache-dtype:fp8_e5m2"],
            )
        self.assertEqual(out.max_model_len, 4096)
        self.assertIs(out.enable_prefix_caching, True)
        self.assertEqual(out.kv_cache_dtype, "fp8_e5m2")


class TestNormalizeOptionKey(unittest.TestCase):
    def test_cli_flag_becomes_a_field_name(self):
        self.assertEqual(normalize_option_key("--reasoning-parser"), "reasoning_parser")

    def test_plain_key_is_untouched(self):
        self.assertEqual(normalize_option_key("tool_parser"), "tool_parser")

    def test_surrounding_whitespace_is_stripped(self):
        self.assertEqual(normalize_option_key("  --tool-parser "), "tool_parser")


if __name__ == "__main__":
    unittest.main()
