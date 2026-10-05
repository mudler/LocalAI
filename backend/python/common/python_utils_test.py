"""Unit tests for the shared python backend helpers (python_utils.py).

Run standalone (Python standard library only, no backend venv needed):
    python3 -m unittest python_utils_test

These mirror the server-less helper tests in backend/python/mlx/test.py
(TestSharedHelpers), but live here so they run on any platform: the mlx
test module imports grpc/backend_pb2 at import time and needs the MLX venv,
whereas python_utils has no third-party dependency. Proto Message objects
are faked with types.SimpleNamespace (real proto fields default to "").
"""

import json
import types
import unittest

from python_utils import attach_media_parts, messages_to_dicts, parse_options


def _msg(**fields):
    """Fake a proto Message: every unset field is the empty string, as protobuf."""
    defaults = {
        "role": "",
        "content": "",
        "name": "",
        "tool_call_id": "",
        "reasoning_content": "",
        "tool_calls": "",
    }
    defaults.update(fields)
    return types.SimpleNamespace(**defaults)


class TestParseOptions(unittest.TestCase):
    def test_type_inference(self):
        opts = parse_options(
            ["temperature:0.7", "max_tokens:128", "trust:true", "name:hello", "no_colon_skipped"]
        )
        self.assertEqual(opts["temperature"], 0.7)
        self.assertEqual(opts["max_tokens"], 128)
        self.assertIs(opts["trust"], True)
        self.assertEqual(opts["name"], "hello")
        self.assertNotIn("no_colon_skipped", opts)


class TestMessagesToDicts(unittest.TestCase):
    def test_basic_fields(self):
        out = messages_to_dicts(
            [
                _msg(role="user", content="hi"),
                _msg(role="tool", content="42", tool_call_id="call_1", name="f"),
            ]
        )
        self.assertEqual(out[0], {"role": "user", "content": "hi"})
        self.assertEqual(out[1]["tool_call_id"], "call_1")
        self.assertEqual(out[1]["name"], "f")

    def test_tool_call_arguments_string_decoded_to_mapping(self):
        # OpenAI wire format ships function.arguments as a JSON *string*; chat
        # templates iterate it as a mapping, so it must come back as a dict.
        out = messages_to_dicts(
            [
                _msg(
                    role="assistant",
                    tool_calls=json.dumps(
                        [
                            {
                                "id": "call_1",
                                "type": "function",
                                "function": {
                                    "name": "get_weather",
                                    "arguments": '{"location": "Rome"}',
                                },
                            }
                        ]
                    ),
                )
            ]
        )
        args = out[0]["tool_calls"][0]["function"]["arguments"]
        self.assertEqual(args, {"location": "Rome"})
        self.assertEqual(dict(args.items()), {"location": "Rome"})

    def test_tool_call_arguments_already_mapping_is_idempotent(self):
        out = messages_to_dicts(
            [
                _msg(
                    role="assistant",
                    tool_calls=json.dumps(
                        [{"function": {"name": "f", "arguments": {"a": 1}}}]
                    ),
                )
            ]
        )
        self.assertEqual(out[0]["tool_calls"][0]["function"]["arguments"], {"a": 1})

    def test_tool_call_arguments_invalid_json_left_as_string(self):
        out = messages_to_dicts(
            [
                _msg(
                    role="assistant",
                    tool_calls=json.dumps(
                        [{"function": {"name": "f", "arguments": "not-json"}}]
                    ),
                )
            ]
        )
        self.assertEqual(out[0]["tool_calls"][0]["function"]["arguments"], "not-json")

    def test_tool_call_without_function_key(self):
        out = messages_to_dicts(
            [_msg(role="assistant", tool_calls=json.dumps([{"id": "call_1"}]))]
        )
        self.assertEqual(out[0]["tool_calls"], [{"id": "call_1"}])

    def test_tool_calls_invalid_json_dropped(self):
        out = messages_to_dicts([_msg(role="assistant", tool_calls="{not json")])
        self.assertNotIn("tool_calls", out[0])


class TestAttachMediaParts(unittest.TestCase):
    def test_image_marker_added_to_last_user_turn(self):
        messages = [
            {"role": "system", "content": "be brief"},
            {"role": "user", "content": "first"},
            {"role": "assistant", "content": "ok"},
            {"role": "user", "content": "how high is the water?"},
        ]
        out = attach_media_parts(messages, n_images=1)
        self.assertEqual(
            out[3]["content"],
            [{"type": "image"}, {"type": "text", "text": "how high is the water?"}],
        )
        # Earlier turns and the input list itself are untouched.
        self.assertEqual(out[:3], messages[:3])
        self.assertEqual(messages[3]["content"], "how high is the water?")

    def test_counts_and_order_images_then_videos(self):
        out = attach_media_parts(
            [{"role": "user", "content": "describe"}], n_images=2, n_videos=1
        )
        self.assertEqual(
            out[0]["content"],
            [
                {"type": "image"},
                {"type": "image"},
                {"type": "video"},
                {"type": "text", "text": "describe"},
            ],
        )

    def test_empty_text_yields_media_only_parts(self):
        out = attach_media_parts([{"role": "user", "content": ""}], n_images=1)
        self.assertEqual(out[0]["content"], [{"type": "image"}])

    def test_other_message_keys_are_preserved(self):
        out = attach_media_parts(
            [{"role": "user", "content": "hi", "name": "bob"}], n_images=1
        )
        self.assertEqual(out[0]["name"], "bob")

    def test_no_media_is_a_no_op(self):
        self.assertIsNone(attach_media_parts([{"role": "user", "content": "hi"}]))

    def test_no_user_turn_is_a_no_op(self):
        self.assertIsNone(
            attach_media_parts([{"role": "system", "content": "hi"}], n_images=1)
        )

    def test_content_already_parts_is_a_no_op(self):
        self.assertIsNone(
            attach_media_parts(
                [{"role": "user", "content": [{"type": "text", "text": "hi"}]}],
                n_images=1,
            )
        )


if __name__ == "__main__":
    unittest.main()
