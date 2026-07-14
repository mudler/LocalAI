# SPDX-License-Identifier: MIT

import importlib.util
import json
import os
import sys
import tempfile
import unittest

BACKEND_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BACKEND_DIR)

# longcat-video is a backend directory, not an importable Python package name.
from longcat_utils import (  # noqa: E402
    MODEL_KIND_AVATAR,
    MODEL_KIND_BASE,
    attention_overrides,
    avatar_segments_for_duration,
    avatar_segments_for_frames,
    classify_model,
    normalize_model_source,
    normalize_num_frames,
    parse_options,
    select_known_options,
    validate_dimensions,
)


SOURCE_DIR = os.path.join(BACKEND_DIR, "sources", "LongCat-Video")
try:
    import torch

    sys.path.insert(0, SOURCE_DIR)
    ATTENTION_TESTS_AVAILABLE = (
        os.path.isdir(SOURCE_DIR) and importlib.util.find_spec("triton") is not None
    )
except ImportError:
    torch = None
    ATTENTION_TESTS_AVAILABLE = False

AVATAR_ATTENTION_TESTS_AVAILABLE = ATTENTION_TESTS_AVAILABLE and all(
    importlib.util.find_spec(module) is not None
    for module in ("pyloudnorm", "scipy", "torchvision")
)


class LongCatUtilsTest(unittest.TestCase):
    def test_parse_options_preserves_colons_and_coerces_scalars(self):
        options = parse_options(
            [
                "use_distill:true",
                "max_segments:4",
                "audio_guidance_scale:3.5",
                "source:https://example.com/model",
                "flag",
            ]
        )

        self.assertEqual(options["use_distill"], True)
        self.assertEqual(options["max_segments"], 4)
        self.assertEqual(options["audio_guidance_scale"], 3.5)
        self.assertEqual(options["source"], "https://example.com/model")
        self.assertEqual(options["flag"], True)

    def test_select_known_options_keeps_known_and_reports_unknown(self):
        # The server injects llama.cpp serving defaults (cache_reuse, parallel)
        # onto every model config. longcat-video must ignore what it does not
        # understand rather than refuse to load - matching every other backend.
        options = {
            "use_distill": True,
            "max_segments": 4,
            "cache_reuse": 256,
            "parallel": 1,
        }
        kept, ignored = select_known_options(options, {"use_distill", "max_segments"})

        self.assertEqual(kept, {"use_distill": True, "max_segments": 4})
        self.assertEqual(ignored, ["cache_reuse", "parallel"])

    def test_select_known_options_reports_nothing_when_all_known(self):
        kept, ignored = select_known_options({"use_distill": True}, {"use_distill"})
        self.assertEqual(kept, {"use_distill": True})
        self.assertEqual(ignored, [])

    def test_classify_model_accepts_only_supported_longcat_models(self):
        cases = {
            "meituan-longcat/LongCat-Video": MODEL_KIND_BASE,
            "https://huggingface.co/meituan-longcat/LongCat-Video": MODEL_KIND_BASE,
            "hf://meituan-longcat/LongCat-Video-Avatar-1.5": MODEL_KIND_AVATAR,
            "other-org/LongCat-Video": None,
            "meituan-longcat/LongCat-Video-Avatar": None,
            "some-org/unrelated-model": None,
        }

        for model, expected in cases.items():
            with self.subTest(model=model):
                self.assertEqual(classify_model(model), expected)

    def test_classify_model_reads_local_checkpoint_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            with open(
                os.path.join(directory, "model_index.json"),
                "w",
                encoding="utf-8",
            ) as config_file:
                json.dump({"model_name": "LongCat-Video-Avatar-1.5"}, config_file)

            self.assertEqual(classify_model(directory), MODEL_KIND_AVATAR)

    def test_normalize_model_source_handles_huggingface_uri_forms(self):
        self.assertEqual(
            normalize_model_source(
                "https://huggingface.co/meituan-longcat/LongCat-Video/tree/main"
            ),
            "meituan-longcat/LongCat-Video",
        )
        self.assertEqual(
            normalize_model_source("huggingface://meituan-longcat/LongCat-Video"),
            "meituan-longcat/LongCat-Video",
        )

    def test_frame_and_segment_rounding_matches_longcat_temporal_shape(self):
        self.assertEqual(normalize_num_frames(94), 93)
        self.assertEqual(normalize_num_frames(0), 93)
        self.assertEqual(avatar_segments_for_frames(93), 1)
        self.assertEqual(avatar_segments_for_frames(94), 2)
        self.assertEqual(avatar_segments_for_frames(173), 2)
        self.assertEqual(avatar_segments_for_frames(174), 3)
        self.assertEqual(avatar_segments_for_duration(10.0), 3)

    def test_dimensions_are_bounded_and_aligned(self):
        self.assertEqual(validate_dimensions(0, 0), (832, 480))
        self.assertEqual(validate_dimensions(512, 512), (512, 512))
        with self.assertRaisesRegex(ValueError, "divisible by 16"):
            validate_dimensions(513, 512)
        with self.assertRaisesRegex(ValueError, "must not exceed"):
            validate_dimensions(1920, 1080)

    def test_attention_backend_validation(self):
        self.assertEqual(
            attention_overrides("sdpa"),
            {
                "enable_flashattn2": False,
                "enable_flashattn3": False,
                "enable_xformers": False,
            },
        )
        with self.assertRaisesRegex(ValueError, "attention_backend"):
            attention_overrides("unknown")


@unittest.skipUnless(
    ATTENTION_TESTS_AVAILABLE,
    "patched LongCat source and torch are required for attention tests",
)
class SDPAFallbackTest(unittest.TestCase):
    def test_base_self_attention_matches_reference(self):
        from longcat_video.modules.attention import Attention

        dim, heads, sequence = 64, 4, 32
        attention = Attention(
            dim,
            heads,
            enable_flashattn2=False,
            enable_flashattn3=False,
            enable_xformers=False,
            enable_bsa=False,
        ).float()
        query = torch.randn(2, heads, sequence, dim // heads)
        key = torch.randn_like(query)
        value = torch.randn_like(query)

        output = attention._process_attn(query, key, value, shape=(1, 1, sequence))
        reference = (
            torch.softmax(
                (query @ key.transpose(-1, -2)) * attention.scale,
                dim=-1,
            )
            @ value
        )

        self.assertLess((output - reference).abs().max().item(), 1e-4)

    @unittest.skipUnless(
        AVATAR_ATTENTION_TESTS_AVAILABLE,
        "avatar audio dependencies are required for the avatar attention test",
    )
    def test_avatar_self_attention_matches_reference(self):
        from longcat_video.modules.avatar.attention import Attention

        dim, heads, sequence = 64, 4, 16
        attention = Attention(
            dim,
            heads,
            enable_flashattn2=False,
            enable_flashattn3=False,
            enable_xformers=False,
        ).float()
        query = torch.randn(1, heads, sequence, dim // heads)
        key = torch.randn_like(query)
        value = torch.randn_like(query)

        output = attention._process_attn(query, key, value, shape=(1, 1, sequence))
        reference = (
            torch.softmax(
                (query @ key.transpose(-1, -2)) * attention.scale,
                dim=-1,
            )
            @ value
        )

        self.assertLess((output - reference).abs().max().item(), 1e-4)

    def test_base_cross_attention_remains_block_diagonal(self):
        from longcat_video.modules.attention import MultiHeadCrossAttention

        dim, heads = 64, 4
        attention = MultiHeadCrossAttention(
            dim,
            heads,
            enable_flashattn2=False,
            enable_flashattn3=False,
            enable_xformers=False,
        ).float()
        query = torch.randn(2, 8, dim)
        key_lengths = [5, 7]
        condition = torch.randn(1, sum(key_lengths), dim)

        first = attention._process_cross_attn(query, condition, key_lengths)
        changed = condition.clone()
        changed[:, key_lengths[0] :] = torch.randn_like(changed[:, key_lengths[0] :])
        second = attention._process_cross_attn(query, changed, key_lengths)

        self.assertLess((first[0] - second[0]).abs().max().item(), 1e-5)
        self.assertGreater((first[1] - second[1]).abs().max().item(), 1e-3)


if __name__ == "__main__":
    unittest.main()
