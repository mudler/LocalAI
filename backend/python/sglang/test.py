"""Unit tests for the sglang backend.

Helper-level tests run without launching the gRPC server or loading model
weights — they only exercise the pure-Python helpers on
``BackendServicer``. They do still require ``sglang`` to be importable
because ``_apply_engine_args`` validates keys against
``ServerArgs``'s dataclass fields.
"""
import unittest


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


if __name__ == "__main__":
    unittest.main()
