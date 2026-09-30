import types
import unittest

from tf_engine import Delta, GenerationCancelled, InvalidRequest
from tf_mlx import MlxEngine


class RequestError(Exception):
    pass


class RequestCancelled(Exception):
    pass


class FakeCancellation:
    def __init__(self):
        self.cancelled = False

    def cancel(self):
        self.cancelled = True


class FakePolicy:
    single = False

    def __init__(self, body):
        pass

    def delta(self, d):
        return d

    def flush(self):
        return ""

    def finish(self, reply, tools, parse):
        return reply


def make_helpers(**over):
    h = types.SimpleNamespace(
        parse_numbers=lambda body: body,
        validate_modalities=lambda body: None,
        normalize_messages=lambda messages, allow_images=False: messages,
        active_tool_specs=lambda tools, choice: list(tools or []),
        tool_choice_requires_call=lambda choice: choice == "required",
        thinking_fields=lambda body, levels=frozenset(): (
            {"enable_thinking": body["chat_template_kwargs"]["enable_thinking"]}
            if "chat_template_kwargs" in body else {}),
        parse_tool_calls_from_content=lambda text, tools, max_calls=None: (text, None),
        ToolCallPolicy=FakePolicy,
        Cancellation=FakeCancellation,
        RequestCancelled=RequestCancelled,
        RequestError=RequestError,
        grammar=types.SimpleNamespace(FIELDS=("guided_json", "guided_grammar"), refusal=lambda body, app=None: None),
    )
    for k, v in over.items():
        setattr(h, k, v)
    return h


class FakeApp:
    accepts_sampling = True
    accepts_cancellation = True
    streams_prose_with_tools = True
    vision = None
    effort_levels = frozenset()

    def __init__(self, reply=None, deltas=(), raises=None):
        self.reply = reply or {"content": "ok", "reasoning": None, "finish_reason": "stop",
                               "prompt_tokens": 3, "completion_tokens": 2, "cached_tokens": 1}
        self.deltas = deltas
        self.raises = raises
        self.calls = []

    def chat(self, messages, **kw):
        self.calls.append((messages, kw))
        if self.raises:
            raise self.raises
        cb = kw.get("on_delta")
        if cb:
            for d in self.deltas:
                cb(d)
        return dict(self.reply)


MSG = [{"role": "user", "content": "hi"}]


class MlxEngineTest(unittest.TestCase):
    def test_result_carries_text_and_usage(self):
        engine = MlxEngine(FakeApp(), make_helpers())
        result = engine.generate({"messages": MSG}, True, None, lambda: False)
        self.assertEqual((result.content, result.prompt_tokens, result.completion_tokens, result.cached_tokens),
                         ("ok", 3, 2, 1))

    def test_sampling_fields_and_thinking_reach_chat(self):
        app = FakeApp()
        MlxEngine(app, make_helpers()).generate(
            {"messages": MSG, "temperature": 0.5, "top_k": 7, "max_tokens": 9,
             "chat_template_kwargs": {"enable_thinking": True}}, True, None, lambda: False)
        _, kw = app.calls[0]
        self.assertEqual(kw["max_tokens"], 9)
        self.assertEqual(kw["temperature"], 0.5)
        self.assertEqual(kw["sampling"]["top_k"], 7)
        self.assertTrue(kw["sampling"]["enable_thinking"])

    def test_raw_completion_passes_prompt_and_no_messages(self):
        app = FakeApp()
        MlxEngine(app, make_helpers()).generate({"prompt": "once"}, False, None, lambda: False)
        messages, kw = app.calls[0]
        self.assertEqual(messages, [])
        self.assertEqual(kw["prompt"], "once")

    def test_string_and_reasoning_deltas_are_forwarded_in_order(self):
        app = FakeApp(deltas=[{"reasoning_content": "think"}, "an", "swer"])
        seen = []
        MlxEngine(app, make_helpers()).generate({"messages": MSG}, True, seen.append, lambda: False)
        self.assertEqual(seen, [Delta(reasoning="think"), Delta(content="an"), Delta(content="swer")])

    def test_tool_calls_come_from_the_finished_reply(self):
        calls = [{"id": "c1", "function": {"name": "f", "arguments": "{}"}}]

        class Policy(FakePolicy):
            def finish(self, reply, tools, parse):
                return {**reply, "tool_calls": calls, "finish_reason": "tool_calls"}

        tools = [{"type": "function", "function": {"name": "f"}}]
        engine = MlxEngine(FakeApp(), make_helpers(ToolCallPolicy=Policy))
        result = engine.generate({"messages": MSG, "tools": tools}, True, None, lambda: False)
        self.assertEqual(result.finish_reason, "tool_calls")
        self.assertEqual(result.tool_calls[0].name, "f")

    def test_required_tool_choice_sets_tool_call_required(self):
        app = FakeApp()
        tools = [{"type": "function", "function": {"name": "f"}}]
        MlxEngine(app, make_helpers()).generate(
            {"messages": MSG, "tools": tools, "tool_choice": "required"}, True, None, lambda: False)
        self.assertTrue(app.calls[0][1]["sampling"]["tool_call_required"])

    def test_request_errors_become_invalid_request(self):
        engine = MlxEngine(FakeApp(raises=RequestError("bad grammar")), make_helpers())
        with self.assertRaises(InvalidRequest):
            engine.generate({"messages": MSG}, True, None, lambda: False)

    def test_grammar_refusal_is_invalid_request(self):
        helpers = make_helpers(grammar=types.SimpleNamespace(FIELDS=(), refusal=lambda body, app=None: "no grammar"))
        with self.assertRaises(InvalidRequest):
            MlxEngine(FakeApp(), helpers).generate({"messages": MSG}, True, None, lambda: False)

    def test_images_without_vision_are_rejected(self):
        content = [{"type": "text", "text": "x"}, {"type": "image_url", "image_url": {"url": "data:image/png;base64,AA"}}]

        def normalize(messages, allow_images=False):
            if not allow_images:
                raise RequestError("images are unsupported")
            return messages

        engine = MlxEngine(FakeApp(), make_helpers(normalize_messages=normalize))
        with self.assertRaises(InvalidRequest):
            engine.generate({"messages": [{"role": "user", "content": content}]}, True, None, lambda: False)

    def test_client_disconnect_cancels_the_upstream_request(self):
        state = {"gone": False}

        class LeavingApp(FakeApp):
            def chat(self, messages, **kw):
                kw["on_delta"]("a")
                state["gone"] = True
                kw["on_delta"]("b")     # the adapter polls is_cancelled on every delta
                if kw["cancellation"].cancelled:
                    raise RequestCancelled("client left")
                return dict(self.reply)

        engine = MlxEngine(LeavingApp(), make_helpers())
        with self.assertRaises(GenerationCancelled):
            engine.generate({"messages": MSG}, True, lambda d: None, lambda: state["gone"])

    def test_tokenize_uses_the_app_tokenizer(self):
        app = FakeApp()
        app.tokenizer = types.SimpleNamespace(encode=lambda text: [1, 2, 3])
        self.assertEqual(MlxEngine(app, make_helpers()).tokenize("abc"), [1, 2, 3])
