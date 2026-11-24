"""
Tests for reward_functions.py — no gRPC / model deps, pure stdlib.

Covers the inline-code opt-in gate (issue #11015): inline reward code is
arbitrary code execution and must stay disabled unless the operator opts in.
"""
import os
import unittest

import reward_functions as rf

INLINE_SPEC = [{"type": "inline", "name": "ok", "code": "return [1.0 for _ in completions]"}]


class TestInlineRewardGate(unittest.TestCase):
    def setUp(self):
        self._saved = os.environ.pop(rf.ALLOW_INLINE_ENV, None)

    def tearDown(self):
        if self._saved is None:
            os.environ.pop(rf.ALLOW_INLINE_ENV, None)
        else:
            os.environ[rf.ALLOW_INLINE_ENV] = self._saved

    def test_inline_refused_by_default(self):
        with self.assertRaises(ValueError) as cm:
            rf.build_reward_functions(INLINE_SPEC)
        self.assertIn("disabled by default", str(cm.exception))

    def test_builtin_works_without_optin(self):
        fns = rf.build_reward_functions([{"type": "builtin", "name": "format_reward"}])
        self.assertEqual(len(fns), 1)
        self.assertTrue(callable(fns[0]))

    def test_inline_enabled_with_optin(self):
        os.environ[rf.ALLOW_INLINE_ENV] = "true"
        fns = rf.build_reward_functions(INLINE_SPEC)
        self.assertEqual(fns[0](["x"]), [1.0])

    def test_optin_falsey_values_stay_disabled(self):
        for val in ("", "0", "false", "no", "off"):
            os.environ[rf.ALLOW_INLINE_ENV] = val
            with self.assertRaises(ValueError):
                rf.build_reward_functions(INLINE_SPEC)


if __name__ == "__main__":
    unittest.main()
