import base64
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))

import backend_pb2
from tf_request import build_body

PNG = b"\x89PNG\r\n\x1a\n" + b"0" * 8


def chat_request(**kwargs):
    return backend_pb2.PredictOptions(
        UseTokenizerTemplate=True,
        Messages=[backend_pb2.Message(role="user", content="hi")],
        **kwargs,
    )


class BuildBodyTest(unittest.TestCase):
    def test_chat_request_carries_messages(self):
        built = build_body(chat_request())
        self.assertTrue(built.chat)
        self.assertEqual(built.body["messages"], [{"role": "user", "content": "hi"}])

    def test_prompt_without_template_is_a_raw_completion(self):
        built = build_body(backend_pb2.PredictOptions(Prompt="once upon"))
        self.assertFalse(built.chat)
        self.assertEqual(built.body["prompt"], "once upon")
        self.assertNotIn("messages", built.body)

    def test_empty_request_is_rejected(self):
        with self.assertRaises(ValueError):
            build_body(backend_pb2.PredictOptions())

    def test_zero_sampling_fields_are_unset_not_greedy(self):
        # LocalAI sends 0 for "not configured"; forcing temperature 0 would make
        # CUDA models (default temperature 1.0) greedy.
        body = build_body(chat_request(Temperature=0.0, TopP=0.0, TopK=0, MinP=0.0, Tokens=0)).body
        for key in ("temperature", "top_p", "top_k", "min_p", "max_tokens", "seed"):
            self.assertNotIn(key, body)

    def test_positive_sampling_fields_are_forwarded(self):
        body = build_body(
            chat_request(Temperature=0.7, TopP=0.9, TopK=40, MinP=0.05, Tokens=128, Seed=7)
        ).body
        self.assertAlmostEqual(body["temperature"], 0.7, places=5)
        self.assertAlmostEqual(body["top_p"], 0.9, places=5)
        self.assertEqual(body["top_k"], 40)
        self.assertAlmostEqual(body["min_p"], 0.05, places=5)
        self.assertEqual(body["max_tokens"], 128)
        self.assertEqual(body["seed"], 7)

    def test_stop_prompts_and_ignore_eos(self):
        body = build_body(chat_request(StopPrompts=["END"], IgnoreEOS=True)).body
        self.assertEqual(body["stop"], ["END"])
        self.assertTrue(body["ignore_eos"])

    def test_tools_and_tool_choice_are_decoded(self):
        tools = [{"type": "function", "function": {"name": "f", "parameters": {}}}]
        body = build_body(chat_request(Tools=json.dumps(tools), ToolChoice="required")).body
        self.assertEqual(body["tools"], tools)
        self.assertEqual(body["tool_choice"], "required")
        body = build_body(chat_request(Tools=json.dumps(tools), ToolChoice='{"type":"function","function":{"name":"f"}}')).body
        self.assertEqual(body["tool_choice"]["function"]["name"], "f")

    def test_enable_thinking_metadata_reaches_chat_template_kwargs(self):
        body = build_body(chat_request(Metadata={"enable_thinking": "true"})).body
        self.assertEqual(body["chat_template_kwargs"], {"enable_thinking": True})
        body = build_body(chat_request(Metadata={"enable_thinking": "false"})).body
        self.assertEqual(body["chat_template_kwargs"], {"enable_thinking": False})

    def test_reasoning_effort_metadata(self):
        body = build_body(chat_request(Metadata={"reasoning_effort": "low"})).body
        self.assertEqual(body["reasoning_effort"], "low")

    def test_json_grammar_becomes_guided_json(self):
        schema = {"type": "object", "properties": {"a": {"type": "string"}}}
        body = build_body(chat_request(Grammar=json.dumps(schema))).body
        self.assertEqual(body["guided_json"], schema)

    def test_non_json_grammar_becomes_guided_grammar(self):
        body = build_body(chat_request(Grammar='root ::= "a"')).body
        self.assertEqual(body["guided_grammar"], 'root ::= "a"')

    def test_base64_image_is_attached_to_the_last_user_message(self):
        encoded = base64.b64encode(PNG).decode()
        body = build_body(chat_request(Images=[encoded])).body
        content = body["messages"][-1]["content"]
        self.assertEqual(content[0], {"type": "text", "text": "hi"})
        self.assertEqual(content[1]["type"], "image_url")
        self.assertEqual(content[1]["image_url"]["url"], "data:image/png;base64," + encoded)

    def test_data_url_image_passes_through(self):
        url = "data:image/png;base64," + base64.b64encode(PNG).decode()
        body = build_body(chat_request(Images=[url])).body
        self.assertEqual(body["messages"][-1]["content"][1]["image_url"]["url"], url)

    def test_image_file_path_is_not_read(self):
        import tempfile

        with tempfile.NamedTemporaryFile(suffix=".png", delete=False) as handle:
            handle.write(PNG)
        try:
            with self.assertRaises(ValueError) as caught:
                build_body(chat_request(Images=[handle.name]))
        finally:
            os.unlink(handle.name)
        self.assertIn("base64", str(caught.exception))

    def test_http_image_url_is_rejected(self):
        with self.assertRaises(ValueError) as caught:
            build_body(chat_request(Images=["https://example.com/cat.png"]))
        self.assertIn("URL", str(caught.exception))
