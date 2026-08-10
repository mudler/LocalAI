# SPDX-License-Identifier: MIT

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from mlx_video_utils import (  # noqa: E402
    build_generation_command,
    classify_model,
    parse_options,
)


class MLXVideoUtilsTest(unittest.TestCase):
    def test_parses_model_options_and_preserves_colons(self):
        self.assertEqual(
            parse_options(["pipeline:dev", "tiling:aggressive", "flag"]),
            {"pipeline": "dev", "tiling": "aggressive", "flag": "true"},
        )

    def test_classifies_supported_model_families(self):
        self.assertEqual(classify_model("Lightricks/LTX-2"), "ltx2")
        self.assertEqual(classify_model("/models/Wan2.2-TI2V-5B-mlx"), "wan")
        self.assertIsNone(classify_model("some-org/unrelated-model"))

    def test_maps_ltx_request_without_invoking_a_shell(self):
        command = build_generation_command(
            family="ltx2",
            model="prince-canuma/LTX-2.3-distilled-mlx",
            prompt="waves; touch /tmp/nope",
            destination="/tmp/out.mp4",
            negative_prompt="blurry",
            start_image="/tmp/start.png",
            end_image="/tmp/end.png",
            width=768,
            height=512,
            num_frames=49,
            fps=24,
            seed=7,
            steps=12,
            cfg_scale=3.5,
            params={"pipeline": "dev", "tiling": "aggressive"},
        )

        self.assertEqual(command[:3], [sys.executable, "-m", "mlx_video.models.ltx_2.generate"])
        self.assertIn("waves; touch /tmp/nope", command)
        self.assertEqual(command[command.index("--model-repo") + 1], "prince-canuma/LTX-2.3-distilled-mlx")
        self.assertEqual(command[command.index("--pipeline") + 1], "dev")
        self.assertEqual(command[command.index("--cfg-scale") + 1], "3.5")
        self.assertIn("--end-image", command)

    def test_maps_wan_request_and_rejects_unsupported_audio(self):
        command = build_generation_command(
            family="wan",
            model="/models/wan22-mlx",
            prompt="a fox",
            destination="/tmp/out.mp4",
            width=1280,
            height=704,
            num_frames=81,
            fps=16,
            seed=9,
            steps=20,
            cfg_scale=4.0,
            params={"scheduler": "unipc", "shift": "5.0"},
        )

        self.assertEqual(command[:3], [sys.executable, "-m", "mlx_video.models.wan_2.generate"])
        self.assertEqual(command[command.index("--model-dir") + 1], "/models/wan22-mlx")
        self.assertEqual(command[command.index("--guide-scale") + 1], "4.0")
        self.assertNotIn("--fps", command)

        with self.assertRaisesRegex(ValueError, "audio conditioning"):
            build_generation_command(
                family="wan",
                model="/models/wan22-mlx",
                prompt="a fox",
                destination="/tmp/out.mp4",
                audio="/tmp/input.wav",
            )


if __name__ == "__main__":
    unittest.main()
