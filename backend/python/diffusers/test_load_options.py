# SPDX-License-Identifier: MIT

import unittest

from backend.python.diffusers.load_options import (
    drop_negative_prompt_if_embedded,
    single_file_load_kwargs,
)


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


class DropNegativePromptTest(unittest.TestCase):
    def test_drops_negative_prompt_when_embeds_present(self):
        kwargs = {"negative_prompt": "blurry", "negative_prompt_embeds": object()}
        drop_negative_prompt_if_embedded(kwargs)
        self.assertNotIn("negative_prompt", kwargs)
        self.assertIn("negative_prompt_embeds", kwargs)

    def test_keeps_negative_prompt_without_embeds(self):
        kwargs = {"negative_prompt": "blurry", "prompt_embeds": object()}
        drop_negative_prompt_if_embedded(kwargs)
        self.assertEqual(kwargs["negative_prompt"], "blurry")

    def test_no_negative_prompt_is_fine(self):
        self.assertEqual(
            drop_negative_prompt_if_embedded({"negative_prompt_embeds": 1}),
            {"negative_prompt_embeds": 1},
        )


if __name__ == "__main__":
    unittest.main()
