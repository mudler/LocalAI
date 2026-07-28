"""Model-identity enforcement for LocalAI Python backends.

In distributed mode the controller caches a routing row naming a backend's
host:port. A worker can recycle a stopped backend's gRPC port for a different
model's backend, and the controller's health probe checks liveness rather than
identity, so the request is dispatched to whatever now occupies the port and
the caller gets a silent wrong-model answer (#10952).

Every request message that reaches a backend through the distributed router
carries a ModelIdentity field naming the model the request is for, so the
backend can reject it at the point of use. This module enforces that for every
Python backend at once: all 36 of them build their server through
grpc_auth.get_auth_interceptors(), so wiring it there needs no per-backend
change. There is no shared BackendServicer base class to hook instead, and the
backends store their loaded model in wildly different attributes, so an
interceptor is the only single point that sees both the LoadModel request and
the inference requests.

Enforcement is deliberately narrow: it compares two strings and never inspects
the model itself.
"""

import asyncio
import inspect
import threading

import grpc

# Must match grpcerrors.ModelMismatchSentinel in pkg/grpc/grpcerrors/errors.go.
# The router requires this substring AND the NOT_FOUND code before it treats a
# reply as a mismatch, because NOT_FOUND alone is not exclusively ours on these
# RPCs (insightface's Embedding returns it for "no face detected").
MODEL_MISMATCH_SENTINEL = "model identity mismatch"

_LOAD_METHOD = "/backend.Backend/LoadModel"

# Every RPC whose request message carries a ModelIdentity field. This set IS
# the enforcement surface for all 36 Python backends: an RPC missing here is
# silently unprotected, so model_identity_test.py pins the full list.
#
# The guard reads request.ModelIdentity generically, so nothing here is
# modality-specific — a backend that does not implement an RPC simply never
# sees it.
#
# TTS and SoundGeneration are guarded on ModelIdentity, NOT on their `model`
# field: the controller's FileStagingClient rewrites `model` to a worker-local
# absolute path, so comparing that would reject valid requests in distributed
# mode. ModelIdentity is a separate, untranslated field for exactly that reason.
#
# AudioEncode/AudioDecode are absent deliberately: the opus codec backend they
# target is loaded from a literal rather than a ModelConfig, so no value carries
# the load-time/request-time equality guarantee this comparison depends on.
_GUARDED_METHODS = frozenset(
    (
        # PredictOptions RPCs (#10970)
        "/backend.Backend/Predict",
        "/backend.Backend/PredictStream",
        "/backend.Backend/Embedding",
        "/backend.Backend/TokenizeString",
        # Remaining modalities
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
)


class ModelIdentityState:
    """The identity this process loaded, and the rule for judging a request.

    A backend process serves exactly one model (worker process keys are
    model+backend+replica), so a single value is enough.
    """

    def __init__(self):
        self._lock = threading.Lock()
        self._loaded = ""

    def record(self, model: str) -> None:
        with self._lock:
            self._loaded = model or ""

    @property
    def loaded(self) -> str:
        with self._lock:
            return self._loaded

    def mismatch(self, requested: str):
        """Return an error message when `requested` names another model.

        Either side being empty means "skip": the request side is empty for a
        controller that predates the field and for internally synthesized
        requests, and the loaded side is empty when such a controller performed
        the load. Neither can judge the other, and a false rejection is worse
        than the miss it prevents.
        """
        if not requested:
            return None
        loaded = self.loaded
        if not loaded or loaded == requested:
            return None
        return "{}: loaded {!r}, requested {!r}".format(
            MODEL_MISMATCH_SENTINEL, loaded, requested
        )


def _rebuild(handler, behavior):
    """Return a copy of `handler` with its behavior replaced.

    Only unary-request handlers are ever passed here: LoadModel and every
    entry in _GUARDED_METHODS take a single request message. The bidirectional
    streams (AudioTranscriptionLive, AudioTransformStream, AudioToAudioStream,
    Forward) are not guarded and never reach this function.
    """
    if handler.response_streaming:
        return grpc.unary_stream_rpc_method_handler(
            behavior,
            request_deserializer=handler.request_deserializer,
            response_serializer=handler.response_serializer,
        )
    return grpc.unary_unary_rpc_method_handler(
        behavior,
        request_deserializer=handler.request_deserializer,
        response_serializer=handler.response_serializer,
    )


class ModelIdentityInterceptor(grpc.ServerInterceptor):
    """Sync interceptor that records the loaded model and guards inference."""

    def __init__(self, state: ModelIdentityState = None):
        self.state = state or ModelIdentityState()

    def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        if method != _LOAD_METHOD and method not in _GUARDED_METHODS:
            return continuation(handler_call_details)

        handler = continuation(handler_call_details)
        if handler is None:
            return handler

        if method == _LOAD_METHOD:
            original = handler.unary_unary

            def record(request, context):
                result = original(request, context)
                # Only a successful load owns the identity; a failed one leaves
                # no model, which the model-not-loaded signal already covers.
                if getattr(result, "success", True):
                    self.state.record(getattr(request, "Model", ""))
                return result

            return _rebuild(handler, record)

        original = handler.unary_stream if handler.response_streaming else handler.unary_unary

        def guard(request, context):
            message = self.state.mismatch(getattr(request, "ModelIdentity", ""))
            if message is not None:
                # abort() raises, so the request never reaches the model.
                context.abort(grpc.StatusCode.NOT_FOUND, message)
            return original(request, context)

        return _rebuild(handler, guard)


_STREAM_DONE = object()


def _next_or_done(iterator):
    """next(iterator), returning the _STREAM_DONE sentinel at exhaustion.

    StopIteration must not propagate out of a function run via run_in_executor:
    it cannot travel through a Future and would surface as an opaque error.
    """
    try:
        return next(iterator)
    except StopIteration:
        return _STREAM_DONE


async def _call_behavior(behavior, request, context):
    """Invoke a unary servicer behavior without blocking the event loop.

    Native async behavior is awaited directly. A sync behavior -- many backends
    define `def LoadModel` / `def Embedding`, not `async def` -- is dispatched to
    a worker thread so a slow load/inference cannot freeze all aio RPC handling,
    mirroring grpc.aio's own sync-handler adaptation. A callable wrapper that
    returns an awaitable is supported too: the (cheap) call runs in the thread,
    then the awaitable is awaited back on the loop.
    """
    if inspect.iscoroutinefunction(behavior):
        return await behavior(request, context)
    loop = asyncio.get_running_loop()
    result = await loop.run_in_executor(None, behavior, request, context)
    if inspect.isawaitable(result):
        result = await result
    return result


class AsyncModelIdentityInterceptor(grpc.aio.ServerInterceptor):
    """Async counterpart for backends running grpc.aio servers."""

    def __init__(self, state: ModelIdentityState = None):
        self.state = state or ModelIdentityState()

    async def intercept_service(self, continuation, handler_call_details):
        method = handler_call_details.method
        if method != _LOAD_METHOD and method not in _GUARDED_METHODS:
            return await continuation(handler_call_details)

        handler = await continuation(handler_call_details)
        if handler is None:
            return handler

        if method == _LOAD_METHOD:
            original = handler.unary_unary

            async def record(request, context):
                # A backend's LoadModel may be a plain sync method (many define
                # `def LoadModel`, not `async def`). Dispatch it so it neither
                # crashes with "object <T> can't be used in 'await'" nor runs its
                # (potentially slow) body on the event loop thread.
                result = await _call_behavior(original, request, context)
                if getattr(result, "success", True):
                    self.state.record(getattr(request, "Model", ""))
                return result

            return _rebuild(handler, record)

        if handler.response_streaming:
            original_stream = handler.unary_stream

            async def guard_stream(request, context):
                message = self.state.mismatch(getattr(request, "ModelIdentity", ""))
                if message is not None:
                    await context.abort(grpc.StatusCode.NOT_FOUND, message)
                # A sync backend yields a plain generator, an async one an async
                # generator. Async: iterate directly. Sync: pull each item via a
                # worker thread so a slow producer doesn't block the event loop
                # (and so StopIteration can't escape through a Future).
                stream = original_stream(request, context)
                if hasattr(stream, "__aiter__"):
                    async for response in stream:
                        yield response
                else:
                    loop = asyncio.get_running_loop()
                    while True:
                        item = await loop.run_in_executor(None, _next_or_done, stream)
                        if item is _STREAM_DONE:
                            break
                        yield item

            return _rebuild(handler, guard_stream)

        original_unary = handler.unary_unary

        async def guard(request, context):
            message = self.state.mismatch(getattr(request, "ModelIdentity", ""))
            if message is not None:
                await context.abort(grpc.StatusCode.NOT_FOUND, message)
            return await _call_behavior(original_unary, request, context)

        return _rebuild(handler, guard)
