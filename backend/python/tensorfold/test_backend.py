import threading
import types
import unittest

import grpc

import backend_pb2
from backend import BackendServicer
from tf_engine import Delta, GenerationCancelled, InvalidRequest, Result, ToolCall
from tf_loader import LoadError


class Context:
    def __init__(self):
        self.code = None
        self.details = None
        self.callbacks = []

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details

    def add_callback(self, callback):
        self.callbacks.append(callback)
        return True

    def disconnect(self):
        for callback in self.callbacks:
            callback()


class FakeEngine:
    def __init__(self, deltas=(), result=None, raises=None):
        self.deltas = deltas
        self.result = result or Result(content="done", prompt_tokens=2, completion_tokens=3)
        self.raises = raises
        self.closed = False
        self.bodies = []

    def generate(self, body, chat, on_delta, is_cancelled):
        self.bodies.append((body, chat))
        if self.raises:
            raise self.raises
        for delta in self.deltas:
            if is_cancelled():
                raise GenerationCancelled("gone")
            if on_delta:
                on_delta(delta)
        return self.result

    def tokenize(self, text):
        return [1, 2, 3]

    def close(self):
        self.closed = True


def loaded_servicer(engine):
    servicer = BackendServicer(loader=lambda cfg: engine)
    result = servicer.LoadModel(backend_pb2.ModelOptions(Model="org/m"), Context())
    assert result.success, result.message
    return servicer


def chat_request(**kw):
    return backend_pb2.PredictOptions(
        UseTokenizerTemplate=True, Messages=[backend_pb2.Message(role="user", content="hi")], **kw)


class BackendServicerTest(unittest.TestCase):
    def test_health(self):
        self.assertEqual(BackendServicer().Health(backend_pb2.HealthMessage(), Context()).message, b"OK")

    def test_load_failure_is_reported_not_raised(self):
        def loader(cfg):
            raise LoadError("Ampere is unsupported")

        result = BackendServicer(loader=loader).LoadModel(backend_pb2.ModelOptions(Model="m"), Context())
        self.assertFalse(result.success)
        self.assertIn("Ampere", result.message)

    def test_unknown_option_fails_the_load(self):
        result = BackendServicer(loader=lambda cfg: FakeEngine()).LoadModel(
            backend_pb2.ModelOptions(Model="m", Options=["prallel:2"]), Context())
        self.assertFalse(result.success)
        self.assertIn("prallel", result.message)

    def test_options_reach_the_loader(self):
        seen = []
        BackendServicer(loader=lambda cfg: (seen.append(cfg), FakeEngine())[1]).LoadModel(
            backend_pb2.ModelOptions(Model="org/m", ContextSize=4096, Options=["parallel:2"]), Context())
        self.assertIn("--parallel", seen[0].argv)
        self.assertIn("4096", seen[0].argv)

    def test_predict_before_load_is_failed_precondition(self):
        ctx = Context()
        BackendServicer().Predict(chat_request(), ctx)
        self.assertEqual(ctx.code, grpc.StatusCode.FAILED_PRECONDITION)

    def test_predict_returns_the_whole_answer(self):
        engine = FakeEngine(result=Result(content="answer", reasoning="why", prompt_tokens=2, completion_tokens=3))
        reply = loaded_servicer(engine).Predict(chat_request(), Context())
        self.assertEqual(reply.message, b"answer")
        self.assertEqual(reply.chat_deltas[0].reasoning_content, "why")
        self.assertEqual((reply.prompt_tokens, reply.tokens), (2, 3))
        self.assertTrue(engine.bodies[0][1])

    def test_invalid_request_maps_to_invalid_argument(self):
        ctx = Context()
        loaded_servicer(FakeEngine(raises=InvalidRequest("bad schema"))).Predict(chat_request(), ctx)
        self.assertEqual(ctx.code, grpc.StatusCode.INVALID_ARGUMENT)
        self.assertIn("bad schema", ctx.details)

    def test_engine_failure_maps_to_internal(self):
        ctx = Context()
        loaded_servicer(FakeEngine(raises=RuntimeError("boom"))).Predict(chat_request(), ctx)
        self.assertEqual(ctx.code, grpc.StatusCode.INTERNAL)

    def test_empty_request_is_invalid_argument(self):
        ctx = Context()
        loaded_servicer(FakeEngine()).Predict(backend_pb2.PredictOptions(), ctx)
        self.assertEqual(ctx.code, grpc.StatusCode.INVALID_ARGUMENT)

    def test_stream_yields_deltas_then_a_closing_reply_with_tool_calls(self):
        result = Result(tool_calls=[ToolCall(0, "call_0", "f", "{}")], prompt_tokens=2, completion_tokens=3,
                        finish_reason="tool_calls")
        engine = FakeEngine(deltas=[Delta(reasoning="t"), Delta(content="a")], result=result)
        replies = list(loaded_servicer(engine).PredictStream(chat_request(), Context()))
        self.assertEqual([r.chat_deltas[0].reasoning_content for r in replies[:2]], ["t", ""])
        self.assertEqual(replies[1].chat_deltas[0].content, "a")
        closing = replies[-1]
        self.assertEqual(closing.chat_deltas[0].tool_calls[0].name, "f")
        self.assertEqual(closing.message, b"")
        self.assertEqual(closing.tokens, 3)

    def test_stream_stops_when_the_client_disconnects(self):
        release = threading.Event()

        class Slow(FakeEngine):
            def generate(self, body, chat, on_delta, is_cancelled):
                on_delta(Delta(content="first"))
                release.wait(2)
                if is_cancelled():
                    raise GenerationCancelled("gone")
                return self.result

        ctx = Context()
        stream = loaded_servicer(Slow()).PredictStream(chat_request(), ctx)
        self.assertEqual(next(stream).chat_deltas[0].content, "first")
        ctx.disconnect()
        release.set()
        self.assertEqual(list(stream), [])

    def test_stream_error_after_start_sets_internal(self):
        ctx = Context()
        list(loaded_servicer(FakeEngine(raises=RuntimeError("boom"))).PredictStream(chat_request(), ctx))
        self.assertEqual(ctx.code, grpc.StatusCode.INTERNAL)

    def test_tokenize_string(self):
        reply = loaded_servicer(FakeEngine()).TokenizeString(backend_pb2.PredictOptions(Prompt="abc"), Context())
        self.assertEqual((reply.length, list(reply.tokens)), (3, [1, 2, 3]))

    def test_free_closes_the_engine_and_unloads(self):
        engine = FakeEngine()
        servicer = loaded_servicer(engine)
        self.assertTrue(servicer.Free(backend_pb2.HealthMessage(), Context()).success)
        self.assertTrue(engine.closed)
        ctx = Context()
        servicer.Predict(chat_request(), ctx)
        self.assertEqual(ctx.code, grpc.StatusCode.FAILED_PRECONDITION)
