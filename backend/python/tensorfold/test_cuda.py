import types
import unittest

from tf_cuda import CudaEngine
from tf_engine import Delta, GenerationCancelled, InvalidRequest


class RequestError(Exception):
    pass


class RequestCancelled(Exception):
    pass


HELPERS = types.SimpleNamespace(RequestError=RequestError, RequestCancelled=RequestCancelled)


class FakeApp:
    def __init__(self, result=None, emits=(), raises=None, prepare_raises=None):
        self.result = result or {
            "final": {}, "calls": None, "finish": "stop", "content": "ok", "reasoning": "",
            "prompt_tokens": 4, "completion_tokens": 2, "cached_tokens": 0, "reasoning_tokens": 0, "stats": {}}
        self.emits, self.raises, self.prepare_raises = emits, raises, prepare_raises
        self.seen = {}
        self.tok = types.SimpleNamespace(encode=lambda text: types.SimpleNamespace(ids=[7, 8]))

    def prepare(self, body, chat):
        if self.prepare_raises:
            raise self.prepare_raises
        return ("prepared", chat)

    def run(self, body, chat, emit, *, prepared=None, cancelled=None):
        self.seen.update(body=body, chat=chat, prepared=prepared, cancelled=cancelled)
        if self.raises:
            raise self.raises
        for delta in self.emits:
            if not emit(delta):
                break
        return self.result


class CudaEngineTest(unittest.TestCase):
    def test_result_maps_usage_and_text(self):
        result = CudaEngine(FakeApp(), HELPERS).generate({"messages": []}, True, None, lambda: False)
        self.assertEqual((result.content, result.finish_reason, result.prompt_tokens, result.completion_tokens),
                         ("ok", "stop", 4, 2))

    def test_prepared_request_is_reused_by_run(self):
        app = FakeApp()
        CudaEngine(app, HELPERS).generate({"messages": []}, True, None, lambda: False)
        self.assertEqual(app.seen["prepared"], ("prepared", True))

    def test_deltas_are_forwarded(self):
        app = FakeApp(emits=[{"reasoning_content": "t"}, {"content": "hi"}])
        seen = []
        CudaEngine(app, HELPERS).generate({"messages": []}, True, seen.append, lambda: False)
        self.assertEqual(seen, [Delta(reasoning="t"), Delta(content="hi")])

    def test_final_delta_is_forwarded_after_the_run(self):
        app = FakeApp(result={**FakeApp().result, "final": {"content": " tail", "reasoning_content": "r"}})
        seen = []
        CudaEngine(app, HELPERS).generate({"messages": []}, True, seen.append, lambda: False)
        self.assertEqual(seen, [Delta(content=" tail", reasoning="r")])

    def test_emit_returns_false_once_cancelled_so_the_engine_stops(self):
        app = FakeApp(emits=[{"content": "a"}, {"content": "b"}])
        seen = []
        state = {"cancelled": False}

        def sink(delta):
            seen.append(delta)
            state["cancelled"] = True

        CudaEngine(app, HELPERS).generate({"messages": []}, True, sink, lambda: state["cancelled"])
        self.assertEqual(seen, [Delta(content="a")])

    def test_tool_calls_are_mapped(self):
        calls = [{"id": "c", "function": {"name": "f", "arguments": "{}"}}]
        app = FakeApp(result={**FakeApp().result, "calls": calls, "finish": "tool_calls"})
        result = CudaEngine(app, HELPERS).generate({"messages": []}, True, None, lambda: False)
        self.assertEqual(result.tool_calls[0].name, "f")
        self.assertEqual(result.finish_reason, "tool_calls")

    def test_request_error_is_invalid_request(self):
        with self.assertRaises(InvalidRequest):
            CudaEngine(FakeApp(prepare_raises=RequestError("bad")), HELPERS).generate(
                {"messages": []}, True, None, lambda: False)

    def test_request_cancelled_is_generation_cancelled(self):
        with self.assertRaises(GenerationCancelled):
            CudaEngine(FakeApp(raises=RequestCancelled("gone")), HELPERS).generate(
                {"messages": []}, True, None, lambda: False)

    def test_tokenize_uses_the_app_tokenizer(self):
        self.assertEqual(CudaEngine(FakeApp(), HELPERS).tokenize("x"), [7, 8])
