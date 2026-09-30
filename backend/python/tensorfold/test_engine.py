import unittest

import backend_pb2
from tf_engine import Delta, Result, ToolCall, tool_calls_from_openai
from tf_reply import final_reply, stream_reply


class ToolCallsFromOpenAITest(unittest.TestCase):
    def test_none_and_empty(self):
        self.assertEqual(tool_calls_from_openai(None), [])
        self.assertEqual(tool_calls_from_openai([]), [])

    def test_string_arguments_pass_through(self):
        calls = tool_calls_from_openai(
            [{"id": "call_a", "type": "function", "function": {"name": "f", "arguments": '{"x":1}'}}]
        )
        self.assertEqual(calls, [ToolCall(index=0, id="call_a", name="f", arguments='{"x":1}')])

    def test_dict_arguments_are_json_encoded(self):
        calls = tool_calls_from_openai([{"function": {"name": "f", "arguments": {"x": 1}}}])
        self.assertEqual(calls[0].arguments, '{"x": 1}')

    def test_null_or_missing_arguments_become_empty(self):
        calls = tool_calls_from_openai(
            [{"function": {"name": "f", "arguments": None}}, {"function": {"name": "g"}}]
        )
        self.assertEqual([c.arguments for c in calls], ["", ""])

    def test_empty_dict_arguments_stay_an_empty_object(self):
        calls = tool_calls_from_openai([{"function": {"name": "f", "arguments": {}}}])
        self.assertEqual(calls[0].arguments, "{}")

    def test_missing_id_is_generated_from_the_index(self):
        calls = tool_calls_from_openai(
            [{"function": {"name": "a", "arguments": ""}}, {"function": {"name": "b", "arguments": ""}}]
        )
        self.assertEqual([c.id for c in calls], ["call_0", "call_1"])
        self.assertEqual([c.index for c in calls], [0, 1])


class ReplyTest(unittest.TestCase):
    def test_stream_reply_carries_content_and_reasoning(self):
        reply = stream_reply(Delta(content="hi", reasoning="hm"))
        self.assertEqual(reply.message, b"hi")
        self.assertEqual(reply.chat_deltas[0].content, "hi")
        self.assertEqual(reply.chat_deltas[0].reasoning_content, "hm")

    def test_final_reply_reports_usage_and_tool_calls(self):
        result = Result(
            content="",
            tool_calls=[ToolCall(0, "call_0", "f", '{"x":1}')],
            finish_reason="tool_calls",
            prompt_tokens=11,
            completion_tokens=5,
        )
        reply = final_reply(result, with_text=False)
        self.assertEqual(reply.prompt_tokens, 11)
        self.assertEqual(reply.tokens, 5)
        self.assertEqual(reply.message, b"")
        call = reply.chat_deltas[0].tool_calls[0]
        self.assertEqual((call.index, call.id, call.name, call.arguments), (0, "call_0", "f", '{"x":1}'))

    def test_final_reply_with_text_is_the_whole_answer(self):
        reply = final_reply(Result(content="answer", reasoning="why"), with_text=True)
        self.assertEqual(reply.message, b"answer")
        self.assertEqual(reply.chat_deltas[0].content, "answer")
        self.assertEqual(reply.chat_deltas[0].reasoning_content, "why")
