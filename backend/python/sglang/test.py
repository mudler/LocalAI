"""Unit tests for the sglang backend.

Helper-level tests run without launching the gRPC server or loading model
weights — they only exercise the pure-Python helpers on
``BackendServicer``. They do still require ``sglang`` to be importable
because ``_apply_engine_args`` validates keys against ``ServerArgs``
(a dataclass up to sglang 0.5.19, a ``msgspec.Struct`` from 0.5.20 on).
"""
import unittest



def _request(metadata=None):
    """Minimal stand-in for a PredictOptions request in reasoning tests."""
    from types import SimpleNamespace

    return SimpleNamespace(Metadata=metadata or {})

class TestSglangHelpers(unittest.TestCase):
    """Tests for the pure helpers on BackendServicer (no gRPC, no engine)."""

    def _servicer(self):
        import sys
        import os
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
        from backend import BackendServicer  # noqa: E402
        return BackendServicer()

    def test_parse_options(self):
        servicer = self._servicer()
        opts = servicer._parse_options([
            "tool_parser:hermes",
            "reasoning_parser:deepseek_r1",
            "invalid_no_colon",
            "key_with_colons:a:b:c",
        ])
        self.assertEqual(opts["tool_parser"], "hermes")
        self.assertEqual(opts["reasoning_parser"], "deepseek_r1")
        self.assertEqual(opts["key_with_colons"], "a:b:c")
        self.assertNotIn("invalid_no_colon", opts)

    def test_apply_engine_args_known_keys(self):
        """User-supplied JSON merges into the kwargs dict; pre-set typed
        fields stay put when not overridden."""
        import json as _json
        servicer = self._servicer()
        base = {
            "model_path": "facebook/opt-125m",
            "mem_fraction_static": 0.7,
        }
        extras = _json.dumps({
            "trust_remote_code": True,
            "speculative_algorithm": "EAGLE",
            "speculative_num_steps": 1,
        })
        out = servicer._apply_engine_args(base, extras)
        self.assertIs(out, base)  # in-place merge — same dict back
        self.assertTrue(out["trust_remote_code"])
        self.assertEqual(out["speculative_algorithm"], "EAGLE")
        self.assertEqual(out["speculative_num_steps"], 1)
        self.assertEqual(out["model_path"], "facebook/opt-125m")
        self.assertEqual(out["mem_fraction_static"], 0.7)

    def test_apply_engine_args_engine_args_overrides_typed_fields(self):
        """engine_args wins over previously-set typed kwargs (vLLM precedence)."""
        import json as _json
        servicer = self._servicer()
        base = {"model_path": "facebook/opt-125m", "mem_fraction_static": 0.7}
        out = servicer._apply_engine_args(
            base, _json.dumps({"mem_fraction_static": 0.5}),
        )
        self.assertEqual(out["mem_fraction_static"], 0.5)

    def test_apply_engine_args_unknown_key_raises(self):
        """Typo'd key raises ValueError with a close-match suggestion."""
        import json as _json
        servicer = self._servicer()
        base = {"model_path": "facebook/opt-125m"}
        with self.assertRaises(ValueError) as ctx:
            servicer._apply_engine_args(
                base, _json.dumps({"trust_remotecode": True}),
            )
        msg = str(ctx.exception)
        self.assertIn("trust_remotecode", msg)
        self.assertIn("trust_remote_code", msg)

    def test_apply_engine_args_msgspec_serverargs(self):
        """sglang >= 0.5.20 exposes ServerArgs as a msgspec.Struct instead of a
        dataclass; the field names then live in ``__struct_fields__``.

        Pinned with a stand-in so the msgspec path is covered no matter which
        sglang version happens to be installed.
        """
        import json as _json
        servicer = self._servicer()
        import backend as backend_mod

        class _StructLikeServerArgs:
            __struct_fields__ = ("model_path", "mem_fraction_static",
                                 "trust_remote_code")

        original = backend_mod.ServerArgs
        backend_mod.ServerArgs = _StructLikeServerArgs
        try:
            out = servicer._apply_engine_args(
                {}, _json.dumps({"trust_remote_code": True}),
            )
            self.assertTrue(out["trust_remote_code"])
            with self.assertRaises(ValueError) as ctx:
                servicer._apply_engine_args(
                    {}, _json.dumps({"mem_fraction_statik": 0.7}),
                )
            msg = str(ctx.exception)
            self.assertIn("mem_fraction_statik", msg)
            self.assertIn("mem_fraction_static", msg)
        finally:
            backend_mod.ServerArgs = original

    def test_apply_engine_args_empty_passthrough(self):
        """Empty / None engine_args returns the kwargs dict untouched."""
        servicer = self._servicer()
        base = {"model_path": "facebook/opt-125m"}
        self.assertIs(servicer._apply_engine_args(base, ""), base)
        self.assertIs(servicer._apply_engine_args(base, None), base)

    def test_apply_engine_args_invalid_json_raises(self):
        servicer = self._servicer()
        with self.assertRaises(ValueError) as ctx:
            servicer._apply_engine_args({}, "not-json")
        self.assertIn("not valid JSON", str(ctx.exception))

    def test_apply_engine_args_non_object_raises(self):
        servicer = self._servicer()
        with self.assertRaises(ValueError) as ctx:
            servicer._apply_engine_args({}, "[1,2,3]")
        self.assertIn("must be a JSON object", str(ctx.exception))

    def test_build_prompt_forwards_enable_thinking(self):
        from types import SimpleNamespace

        class Tok:
            def __init__(self):
                self.kwargs = None

            def apply_chat_template(self, messages, **kwargs):
                self.kwargs = kwargs
                return "PROMPT"

        def kwargs_for(metadata):
            servicer = self._servicer()
            tok = Tok()
            servicer.tokenizer = tok
            msg = SimpleNamespace(
                role="user", content="hi", name="",
                tool_call_id="", reasoning_content="", tool_calls="",
            )
            req = SimpleNamespace(
                Prompt="", UseTokenizerTemplate=True,
                Messages=[msg], Tools="", Metadata=metadata,
            )
            self.assertEqual(servicer._build_prompt(req), "PROMPT")
            return tok.kwargs

        self.assertIs(kwargs_for({"enable_thinking": "true"})["enable_thinking"], True)
        # "false" used to be dropped, so Qwen3 kept thinking on
        self.assertIs(kwargs_for({"enable_thinking": "false"})["enable_thinking"], False)
        self.assertNotIn("enable_thinking", kwargs_for({}))
        self.assertIs(kwargs_for({"enable_thinking": "FALSE"})["enable_thinking"], False)

    def test_reasoning_parser_forced_when_template_prefills_think_tag(self):
        """Qwen3's template puts ``<think>`` in the prompt, so the completion
        never contains it. Without force_reasoning the detector treats the whole
        completion as normal text and reasoning_content stays empty."""
        servicer = self._servicer()
        servicer.reasoning_parser_name = "qwen3"

        # What the model actually emits when the prompt ends in "<think>".
        completion = "adding two and two</think>4"

        forced, require_reasoning = servicer._new_reasoning_parser(
            False, prompt="user: hi\n<think>\n"
        )
        self.assertTrue(require_reasoning)
        reasoning, content = forced.parse_non_stream(completion)
        self.assertEqual(reasoning, "adding two and two")
        self.assertEqual(content, "4")

        # No prefilled tag in the prompt: detector default, unchanged behaviour.
        unforced, require_reasoning = servicer._new_reasoning_parser(False, prompt="user: hi\n")
        self.assertFalse(require_reasoning)
        reasoning, content = unforced.parse_non_stream(completion)
        self.assertFalse(reasoning)
        self.assertEqual(content, completion)

    def test_reasoning_parser_not_forced_when_thinking_is_off(self):
        """Thinking off means no ``<think>`` in the prompt either, so the answer
        must not be swallowed into reasoning_content."""
        servicer = self._servicer()
        servicer.reasoning_parser_name = "qwen3"

        parser, require_reasoning = servicer._new_reasoning_parser(False, prompt="user: primes?\n")
        self.assertFalse(require_reasoning)
        reasoning, content = parser.parse_non_stream("2,3,5,7,11")
        self.assertFalse(reasoning)
        self.assertEqual(content, "2,3,5,7,11")

    def test_grammar_constrained_output_is_not_forced_into_reasoning(self):
        """Structured decoding applies from the first token, so the model cannot
        emit the closing tag even though the template opened the block. The whole
        completion is schema output and must stay in content."""
        servicer = self._servicer()
        servicer.reasoning_parser_name = "qwen3"

        schema_out = '{"findings": [{"line": 42, "issue": "off-by-one"}]}'
        parser, require_reasoning = servicer._new_reasoning_parser(
            False, prompt="audit this\n<think>\n", grammar_constrained=True,
        )
        self.assertFalse(require_reasoning)
        reasoning, content = parser.parse_non_stream(schema_out)
        self.assertFalse(reasoning)
        self.assertEqual(content, schema_out)

    def test_reasoning_parser_absent_without_configured_parser(self):
        servicer = self._servicer()
        servicer.reasoning_parser_name = None
        parser, require_reasoning = servicer._new_reasoning_parser(False, prompt="<think>")
        self.assertIsNone(parser)
        self.assertFalse(require_reasoning)

    def test_reasoning_default_off_applies_when_request_is_silent(self):
        """A model configured with reasoning_default:off must render with
        thinking disabled even when the request carries no enable_thinking -
        that is the whole point: `parameters: reasoning_effort:` never
        reaches this backend, so without this the config lies about the
        default."""
        servicer = self._servicer()
        servicer.reasoning_default = "off"
        self.assertIs(servicer._thinking_default(_request(metadata={})), False)

    def test_request_metadata_overrides_reasoning_default(self):
        """A per-request value always wins over the model-level default -
        in both directions."""
        servicer = self._servicer()
        servicer.reasoning_default = "off"
        self.assertIs(
            servicer._thinking_default(_request(metadata={"enable_thinking": "true"})),
            True,
        )
        servicer.reasoning_default = "on"
        self.assertIs(
            servicer._thinking_default(_request(metadata={"enable_thinking": "false"})),
            False,
        )

    def test_no_reasoning_default_leaves_template_untouched(self):
        """Unconfigured must stay unconfigured: returning None means the
        backend adds no enable_thinking kwarg at all, so the template keeps
        whatever default it ships with."""
        servicer = self._servicer()
        self.assertIsNone(servicer._thinking_default(_request(metadata={})))

    def test_thinking_budget_added_to_sampling_params_as_custom_params(self):
        """The model-level thinking_budget option (set from LoadModel's
        Options, mirroring tool_parser/reasoning_parser) must ride along as
        sampling_params['custom_params']['thinking_budget'] on every
        request — that's the only field sglang's --enable-strict-thinking
        grammar backend reads to bound the reasoning length."""
        from types import SimpleNamespace

        servicer = self._servicer()
        servicer.thinking_budget = 512
        request = SimpleNamespace(
            Temperature=0.7, N=0, PresencePenalty=0, FrequencyPenalty=0,
            RepetitionPenalty=0, TopP=0, TopK=0, MinP=0, Seed=0,
            StopPrompts=[], StopTokenIds=[], IgnoreEOS=False, Tokens=0,
            MinTokens=0, SkipSpecialTokens=False, Grammar="",
        )
        params = servicer._build_sampling_params(request)
        self.assertEqual(params["custom_params"], {"thinking_budget": 512})

    def test_no_thinking_budget_means_no_custom_params_key(self):
        """Unconfigured is unconfigured: no thinking_budget option must not
        add an empty/None custom_params that could clobber a sglang-side
        --preferred-sampling-params default (see sglang#40634)."""
        from types import SimpleNamespace

        servicer = self._servicer()
        request = SimpleNamespace(
            Temperature=0.7, N=0, PresencePenalty=0, FrequencyPenalty=0,
            RepetitionPenalty=0, TopP=0, TopK=0, MinP=0, Seed=0,
            StopPrompts=[], StopTokenIds=[], IgnoreEOS=False, Tokens=0,
            MinTokens=0, SkipSpecialTokens=False, Grammar="",
        )
        params = servicer._build_sampling_params(request)
        self.assertNotIn("custom_params", params)

    def test_thinking_budget_accepts_integral_spellings(self):
        """YAML options arrive as strings; "512" and "512.0" both mean 512."""
        servicer = self._servicer()
        self.assertEqual(servicer._parse_thinking_budget("512"), 512)
        self.assertEqual(servicer._parse_thinking_budget("512.0"), 512)
        self.assertEqual(servicer._parse_thinking_budget(" 64 "), 64)
        self.assertEqual(servicer._parse_thinking_budget(256), 256)

    def test_thinking_budget_unset_is_none(self):
        servicer = self._servicer()
        self.assertIsNone(servicer._parse_thinking_budget(None))
        self.assertIsNone(servicer._parse_thinking_budget(""))

    def test_thinking_budget_zero_and_negative_are_ignored(self):
        """No defined meaning in sglang -- ignored, not passed through."""
        servicer = self._servicer()
        self.assertIsNone(servicer._parse_thinking_budget("0"))
        self.assertIsNone(servicer._parse_thinking_budget("-100"))

    def test_thinking_budget_non_integer_does_not_raise(self):
        """A bad value must not crash LoadModel for the whole model."""
        servicer = self._servicer()
        self.assertIsNone(servicer._parse_thinking_budget("12.5"))
        self.assertIsNone(servicer._parse_thinking_budget("lots"))

    def test_warns_when_budget_set_without_strict_thinking(self):
        servicer = self._servicer()
        self.assertIn(
            "enable_strict_thinking",
            servicer._strict_thinking_warning(512, {"model_path": "x"}),
        )
        self.assertIsNone(servicer._strict_thinking_warning(512, {"enable_strict_thinking": True}))
        self.assertIsNone(servicer._strict_thinking_warning(None, {}))

    def test_explicit_zero_temperature_and_seed_are_preserved(self):
        """Temperature=0 is greedy decoding and 0 is a valid seed — neither is
        an unset value. A dropped seed turns a reproducible request random."""
        from types import SimpleNamespace

        servicer = self._servicer()
        import sys as _sys
        _SEED_KEY_FOR_TEST = _sys.modules["backend"]._SEED_KEY
        request = SimpleNamespace(
            Temperature=0,
            N=0,
            PresencePenalty=0,
            FrequencyPenalty=0,
            RepetitionPenalty=0,
            TopP=0,
            TopK=0,
            MinP=0,
            Seed=0,
            StopPrompts=[],
            StopTokenIds=[],
            IgnoreEOS=False,
            Tokens=0,
            MinTokens=0,
            SkipSpecialTokens=False,
            Grammar="",
        )

        params = servicer._build_sampling_params(request)
        self.assertEqual(params["temperature"], 0)
        self.assertEqual(params[_SEED_KEY_FOR_TEST], 0)
        # Other protobuf-default scalar fields must remain filtered. top_k=0 in
        # particular is not a value sglang accepts (-1 disables it), so it must
        # keep falling through to the engine default.
        self.assertNotIn("top_p", params)
        self.assertNotIn("top_k", params)


if __name__ == "__main__":
    unittest.main()
