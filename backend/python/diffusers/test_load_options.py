# SPDX-License-Identifier: MIT

import unittest

from backend.python.diffusers.load_options import single_file_load_kwargs


class SingleFileLoadKwargsTest(unittest.TestCase):
    def test_includes_original_config_for_single_file(self):
        self.assertEqual(
            single_file_load_kwargs("configs/v1-inference.yaml", True),
            {"original_config_file": "configs/v1-inference.yaml"},
        )

    def test_omits_original_config_for_pretrained_model(self):
        self.assertEqual(
            single_file_load_kwargs("configs/v1-inference.yaml", False),
            {},
        )

    def test_omits_empty_original_config(self):
        self.assertEqual(single_file_load_kwargs("", True), {})


if __name__ == "__main__":
    unittest.main()
