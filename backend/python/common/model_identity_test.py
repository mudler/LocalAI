"""Unit tests for model-identity enforcement (model_identity.py).

Run inside any backend venv (needs grpcio, which every Python backend has):
    python -m unittest model_identity_test

Mirrors the Go coverage in pkg/grpc/model_identity_test.go and
pkg/grpc/grpcerrors/errors_test.go. The rules under test are the ones whose
failure modes are silent: enforcement that is wired up but never installed, and
enforcement that rejects requests it should serve.
"""

import asyncio
import os
import unittest

import grpc

import grpc_auth
import model_identity


class _Aborted(Exception):
    pass


class _FakeContext:
    """Minimal ServicerContext: abort() records and raises, like the real one."""

    def __init__(self):
        self.code = None
        self.details = None

    def abort(self, code, details):
        self.code = code
        self.details = details
        raise _Aborted(details)


class _FakeCallDetails:
    def __init__(self, method):
        self.method = method
        self.invocation_metadata = ()


class _Request:
    """Stands in for ModelOptions / PredictOptions.

    The generated protobuf classes are built at container-build time and are
    not importable here, but the interceptor only ever reads two attributes.
    """

    def __init__(self, Model="", ModelIdentity=""):
        self.Model = Model
        self.ModelIdentity = ModelIdentity


class _Result:
    def __init__(self, success=True):
        self.success = success


def _handler(behavior, response_streaming=False):
    if response_streaming:
        return grpc.unary_stream_rpc_method_handler(behavior)
    return grpc.unary_unary_rpc_method_handler(behavior)


class TestInterceptorInstalled(unittest.TestCase):
    """The wiring, which is where this can silently do nothing.

    get_auth_interceptors() returns early when LOCALAI_GRPC_AUTH_TOKEN is
    unset, which is the DEFAULT configuration. An identity interceptor added
    after that return is never installed on any of the 36 Python backends, and
    nothing else would notice.
    """

    def setUp(self):
        self._saved = os.environ.get("LOCALAI_GRPC_AUTH_TOKEN")
        os.environ.pop("LOCALAI_GRPC_AUTH_TOKEN", None)

    def tearDown(self):
        if self._saved is None:
            os.environ.pop("LOCALAI_GRPC_AUTH_TOKEN", None)
        else:
            os.environ["LOCALAI_GRPC_AUTH_TOKEN"] = self._saved

    def test_installed_when_auth_is_disabled(self):
        interceptors = grpc_auth.get_auth_interceptors()
        self.assertTrue(
            any(isinstance(i, model_identity.ModelIdentityInterceptor) for i in interceptors),
            "identity enforcement must be installed even with gRPC auth off "
            "(the default); got {!r}".format(interceptors),
        )

    def test_installed_when_auth_is_disabled_aio(self):
        interceptors = grpc_auth.get_auth_interceptors(aio=True)
        self.assertTrue(
            any(
                isinstance(i, model_identity.AsyncModelIdentityInterceptor)
                for i in interceptors
            ),
            "async identity enforcement must be installed with gRPC auth off",
        )

    def test_installed_alongside_auth_when_enabled(self):
        os.environ["LOCALAI_GRPC_AUTH_TOKEN"] = "secret"
        interceptors = grpc_auth.get_auth_interceptors()
        self.assertTrue(
            any(isinstance(i, model_identity.ModelIdentityInterceptor) for i in interceptors)
        )
        self.assertTrue(
            any(isinstance(i, grpc_auth.TokenAuthInterceptor) for i in interceptors)
        )


class TestMismatchRule(unittest.TestCase):
    """The pure policy. Every 'serve' case here is a false-rejection guard."""

    def setUp(self):
        self.state = model_identity.ModelIdentityState()

    def test_rejects_a_different_model(self):
        self.state.record("a.gguf")
        message = self.state.mismatch("b.gguf")
        self.assertIsNotNone(message)
        self.assertIn(model_identity.MODEL_MISMATCH_SENTINEL, message)
        self.assertIn("a.gguf", message)
        self.assertIn("b.gguf", message)

    def test_serves_the_same_model(self):
        self.state.record("a.gguf")
        self.assertIsNone(self.state.mismatch("a.gguf"))

    def test_serves_when_the_request_has_no_identity(self):
        self.state.record("a.gguf")
        self.assertIsNone(self.state.mismatch(""))

    def test_serves_when_nothing_was_recorded(self):
        self.assertIsNone(self.state.mismatch("b.gguf"))


class TestInterceptorBehavior(unittest.TestCase):
    def setUp(self):
        self.interceptor = model_identity.ModelIdentityInterceptor()
        self.served = []

    def _intercept(self, method, handler):
        return self.interceptor.intercept_service(
            lambda _: handler, _FakeCallDetails(method)
        )

    def _load(self, model, success=True):
        handler = _handler(lambda request, context: _Result(success=success))
        wrapped = self._intercept("/backend.Backend/LoadModel", handler)
        wrapped.unary_unary(_Request(Model=model), _FakeContext())

    def _call(self, method, identity, response_streaming=False):
        def behavior(request, context):
            self.served.append(method)
            return "served"

        handler = _handler(behavior, response_streaming=response_streaming)
        wrapped = self._intercept(method, handler)
        context = _FakeContext()
        behavior_fn = wrapped.unary_stream if response_streaming else wrapped.unary_unary
        return behavior_fn(_Request(ModelIdentity=identity), context), context

    def test_load_records_the_identity(self):
        self._load("a.gguf")
        self.assertEqual(self.interceptor.state.loaded, "a.gguf")

    def test_failed_load_records_nothing(self):
        self._load("a.gguf", success=False)
        self.assertEqual(self.interceptor.state.loaded, "")

    def test_rejects_every_guarded_rpc_on_mismatch(self):
        self._load("a.gguf")
        for method in sorted(model_identity._GUARDED_METHODS):
            streaming = method.endswith("PredictStream")
            with self.subTest(method=method):
                with self.assertRaises(_Aborted):
                    self._call(method, "b.gguf", response_streaming=streaming)
        self.assertEqual(self.served, [], "no request may reach the model")

    def test_reject_uses_not_found_and_the_sentinel(self):
        self._load("a.gguf")

        def behavior(request, context):
            return "served"

        wrapped = self._intercept(
            "/backend.Backend/Predict", _handler(behavior)
        )
        context = _FakeContext()
        with self.assertRaises(_Aborted):
            wrapped.unary_unary(_Request(ModelIdentity="b.gguf"), context)
        self.assertEqual(context.code, grpc.StatusCode.NOT_FOUND)
        self.assertIn(model_identity.MODEL_MISMATCH_SENTINEL, context.details)

    def test_serves_matching_identity(self):
        self._load("a.gguf")
        for method in sorted(model_identity._GUARDED_METHODS):
            streaming = method.endswith("PredictStream")
            self._call(method, "a.gguf", response_streaming=streaming)
        self.assertEqual(len(self.served), len(model_identity._GUARDED_METHODS))

    def test_serves_request_without_identity(self):
        self._load("a.gguf")
        self._call("/backend.Backend/Predict", "")
        self.assertEqual(self.served, ["/backend.Backend/Predict"])

    def test_serves_when_load_recorded_nothing(self):
        self._call("/backend.Backend/Predict", "b.gguf")
        self.assertEqual(self.served, ["/backend.Backend/Predict"])

    def test_unguarded_rpcs_pass_through_untouched(self):
        handler = _handler(lambda request, context: "served")
        wrapped = self._intercept("/backend.Backend/Health", handler)
        self.assertIs(wrapped, handler)


# Every modality request message now carries a ModelIdentity field, so every
# modality RPC shares the guard. The set below is the enforcement surface for
# all 36 Python backends at once: an RPC missing from it is silently
# unprotected, which is the failure mode this class exists to catch.
_EXPECTED_MODALITY_METHODS = (
    "/backend.Backend/GenerateImage",
    "/backend.Backend/GenerateVideo",
    "/backend.Backend/TTS",
    "/backend.Backend/TTSStream",
    "/backend.Backend/SoundGeneration",
    "/backend.Backend/AudioTranscription",
    "/backend.Backend/AudioTranscriptionStream",
    "/backend.Backend/Detect",
    "/backend.Backend/Depth",
    "/backend.Backend/FaceVerify",
    "/backend.Backend/FaceAnalyze",
    "/backend.Backend/VoiceVerify",
    "/backend.Backend/VoiceAnalyze",
    "/backend.Backend/VoiceEmbed",
    "/backend.Backend/Rerank",
    "/backend.Backend/TokenClassify",
    "/backend.Backend/Score",
    "/backend.Backend/VAD",
    "/backend.Backend/Diarize",
    "/backend.Backend/SoundDetection",
    "/backend.Backend/AudioTransform",
)


class TestModalityMethods(unittest.TestCase):
    def setUp(self):
        self.interceptor = model_identity.ModelIdentityInterceptor()
        self.interceptor.state.record("a.gguf")
        self.served = []

    def _call(self, method, identity, response_streaming=False):
        def behavior(request, context):
            self.served.append(method)
            return "served"

        handler = _handler(behavior, response_streaming=response_streaming)
        wrapped = self.interceptor.intercept_service(
            lambda _: handler, _FakeCallDetails(method)
        )
        fn = wrapped.unary_stream if response_streaming else wrapped.unary_unary
        return fn(_Request(ModelIdentity=identity), _FakeContext())

    def test_every_modality_rpc_is_guarded(self):
        for method in _EXPECTED_MODALITY_METHODS:
            with self.subTest(method=method):
                self.assertIn(
                    method,
                    model_identity._GUARDED_METHODS,
                    "{} is unprotected on all Python backends".format(method),
                )

    def test_every_modality_rpc_rejects_a_mismatch(self):
        for method in _EXPECTED_MODALITY_METHODS:
            streaming = method.endswith("Stream")
            with self.subTest(method=method):
                with self.assertRaises(_Aborted):
                    self._call(method, "b.gguf", response_streaming=streaming)
        self.assertEqual(self.served, [], "no request may reach the model")

    def test_every_modality_rpc_serves_a_match(self):
        for method in _EXPECTED_MODALITY_METHODS:
            streaming = method.endswith("Stream")
            self._call(method, "a.gguf", response_streaming=streaming)
        self.assertEqual(len(self.served), len(_EXPECTED_MODALITY_METHODS))

    # Compatibility: an old controller sends nothing, and the e2e backend suite
    # drives real backends with bare request structs.
    def test_every_modality_rpc_serves_without_an_identity(self):
        for method in _EXPECTED_MODALITY_METHODS:
            streaming = method.endswith("Stream")
            self._call(method, "", response_streaming=streaming)
        self.assertEqual(len(self.served), len(_EXPECTED_MODALITY_METHODS))

    # AudioEncode/AudioDecode stay out: the opus codec backend is loaded from a
    # literal, not a ModelConfig, so no value carries the structural guarantee
    # the comparison depends on.
    def test_codec_rpcs_stay_unguarded(self):
        for method in ("/backend.Backend/AudioEncode", "/backend.Backend/AudioDecode"):
            self.assertNotIn(method, model_identity._GUARDED_METHODS)
class TestAsyncInterceptorBehavior(unittest.TestCase):
    """The grpc.aio counterpart, which had no behavioral coverage.

    AsyncModelIdentityInterceptor wraps a backend's own servicer behavior. That
    behavior may be sync or async: several backends define `def LoadModel` and
    `def Embedding` (not `async def`), and grpc.aio's dispatch adapts both. The
    interceptor invokes the behavior itself, so if it awaits unconditionally it
    breaks every sync method it guards with "object <T> can't be used in
    'await'". These tests exercise both shapes; the sync ones are the regression.
    """

    def setUp(self):
        self.interceptor = model_identity.AsyncModelIdentityInterceptor()

    def _wrap(self, method, handler):
        async def continuation(_):
            return handler

        return asyncio.run(
            self.interceptor.intercept_service(continuation, _FakeCallDetails(method))
        )

    def _load(self, behavior, model):
        wrapped = self._wrap("/backend.Backend/LoadModel", _handler(behavior))
        return asyncio.run(wrapped.unary_unary(_Request(Model=model), _FakeContext()))

    def _call_unary(self, behavior, identity):
        wrapped = self._wrap("/backend.Backend/Predict", _handler(behavior))
        return asyncio.run(
            wrapped.unary_unary(_Request(ModelIdentity=identity), _FakeContext())
        )

    def _drain_stream(self, behavior, identity):
        wrapped = self._wrap(
            "/backend.Backend/PredictStream", _handler(behavior, response_streaming=True)
        )

        async def drain():
            out = []
            async for item in wrapped.unary_stream(
                _Request(ModelIdentity=identity), _FakeContext()
            ):
                out.append(item)
            return out

        return asyncio.run(drain())

    # --- LoadModel: sync behavior is the regression, async must still work ---

    def test_load_records_with_sync_behavior(self):
        self._load(lambda request, context: _Result(), "a.gguf")
        self.assertEqual(self.interceptor.state.loaded, "a.gguf")

    def test_load_records_with_async_behavior(self):
        async def behavior(request, context):
            return _Result()

        self._load(behavior, "a.gguf")
        self.assertEqual(self.interceptor.state.loaded, "a.gguf")

    def test_failed_sync_load_records_nothing(self):
        self._load(lambda request, context: _Result(success=False), "a.gguf")
        self.assertEqual(self.interceptor.state.loaded, "")

    # --- guarded unary: sync and async behaviors both served / rejected ---

    def test_guard_serves_sync_behavior(self):
        self._load(lambda request, context: _Result(), "a.gguf")
        result = self._call_unary(lambda request, context: "served", "a.gguf")
        self.assertEqual(result, "served")

    def test_guard_serves_async_behavior(self):
        self._load(lambda request, context: _Result(), "a.gguf")

        async def behavior(request, context):
            return "served"

        self.assertEqual(self._call_unary(behavior, "a.gguf"), "served")

    def test_guard_rejects_mismatch(self):
        self._load(lambda request, context: _Result(), "a.gguf")
        with self.assertRaises(_Aborted):
            self._call_unary(lambda request, context: "served", "b.gguf")

    # --- guarded stream: sync generator and async generator both work ---

    def test_guard_stream_serves_sync_generator(self):
        self._load(lambda request, context: _Result(), "a.gguf")

        def behavior(request, context):
            yield "a"
            yield "b"

        self.assertEqual(self._drain_stream(behavior, "a.gguf"), ["a", "b"])

    def test_guard_stream_serves_async_generator(self):
        self._load(lambda request, context: _Result(), "a.gguf")

        async def behavior(request, context):
            yield "a"
            yield "b"

        self.assertEqual(self._drain_stream(behavior, "a.gguf"), ["a", "b"])


if __name__ == "__main__":
    unittest.main()
