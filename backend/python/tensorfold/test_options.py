import unittest

from tf_options import FLAGS, parse_load_config


def flag_value(argv, name):
    return argv[argv.index(name) + 1]


class ParseLoadConfigTest(unittest.TestCase):
    def test_minimal_serves_the_model_without_an_update_check(self):
        cfg = parse_load_config("Vontra/Qwen3.8-27B-MLX-4bit", 0, {})
        self.assertEqual(cfg.argv, ("serve", "Vontra/Qwen3.8-27B-MLX-4bit", "--no-update-check"))
        self.assertEqual(cfg.env, {})

    def test_values_map_to_upstream_flags(self):
        cfg = parse_load_config(
            "m", 0, {"parallel": 2, "top_p": 0.9, "drafter": "z-lab/Qwen3.8-27B-DFlash2"}
        )
        self.assertEqual(flag_value(cfg.argv, "--parallel"), "2")
        self.assertEqual(flag_value(cfg.argv, "--top-p"), "0.9")
        self.assertEqual(flag_value(cfg.argv, "--drafter"), "z-lab/Qwen3.8-27B-DFlash2")

    def test_context_size_becomes_context_flag_only_when_positive(self):
        self.assertNotIn("--context", parse_load_config("m", 0, {}).argv)
        self.assertEqual(flag_value(parse_load_config("m", 8192, {}).argv, "--context"), "8192")

    def test_context_option_overrides_context_size(self):
        cfg = parse_load_config("m", 8192, {"context": 0})
        self.assertEqual(flag_value(cfg.argv, "--context"), "0")

    def test_thinking_true_and_false_pick_the_matching_switch(self):
        self.assertIn("--thinking", parse_load_config("m", 0, {"thinking": True}).argv)
        self.assertIn("--no-thinking", parse_load_config("m", 0, {"thinking": False}).argv)

    def test_switch_false_is_omitted(self):
        self.assertNotIn("--no-drafts", parse_load_config("m", 0, {"no_drafts": False}).argv)
        self.assertIn("--no-drafts", parse_load_config("m", 0, {"no_drafts": True}).argv)

    def test_memory_limit_becomes_an_environment_variable(self):
        cfg = parse_load_config("m", 0, {"memory_limit_gb": 96})
        self.assertEqual(cfg.env, {"TENSORFOLD_MEMORY_LIMIT_GB": "96"})

    def test_unknown_key_is_rejected_and_named(self):
        with self.assertRaises(ValueError) as caught:
            parse_load_config("m", 0, {"prallel": 2})
        self.assertIn("prallel", str(caught.exception))

    def test_bool_on_a_value_key_is_rejected(self):
        with self.assertRaises(ValueError):
            parse_load_config("m", 0, {"parallel": True})

    def test_non_bool_on_a_switch_key_is_rejected(self):
        with self.assertRaises(ValueError):
            parse_load_config("m", 0, {"vision": "yes"})

    def test_kv_dtype_is_checked(self):
        with self.assertRaises(ValueError):
            parse_load_config("m", 0, {"kv_dtype": "fp4"})
        cfg = parse_load_config("m", 0, {"kv_dtype": "int8"})
        self.assertEqual(flag_value(cfg.argv, "--kv-dtype"), "int8")

    def test_every_mapped_flag_looks_like_a_long_option(self):
        for key, (flag, kind) in FLAGS.items():
            self.assertTrue(flag.startswith("--"), key)
            self.assertIn(kind, ("value", "switch"), key)
