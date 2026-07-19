"""Model-identity enforcement for LocalAI Python backends.

In distributed mode the controller caches a routing row naming a backend's
host:port. A worker can recycle a stopped backend's gRPC port for a different
model's backend, and the controller's health probe checks liveness rather than
identity, so the request is dispatched to whatever now occupies the port and
the caller gets a silent wrong-model answer (#10952).

PredictOptions.ModelIdentity carries the model the request is for, so the
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

import threading

import grpc

# Must match grpcerrors.ModelMismatchSentinel in pkg/grpc/grpcerrors/errors.go.
# The router requires this substring AND the NOT_FOUND code before it treats a
# reply as a mismatch, because NOT_FOUND alone is not exclusively ours on these
# RPCs (insightface's Embedding returns it for "no face detected").
MODEL_MISMATCH_SENTINEL = "model identity mismatch"

_LOAD_METHOD = "/backend.Backend/LoadModel"

# The four RPCs that carry PredictOptions. Nothing else has an identity field.
# TTS and SoundGeneration are excluded on purpose: their `model` field is
# already rewritten to a worker-local path by the controller's
# FileStagingClient, so comparing it would reject valid requests.
_GUARDED_METHODS = frozenset(
    (
        "/backend.Backend/Predict",
        "/backend.Backend/PredictStream",
        "/backend.Backend/Embedding",
        "/backend.Backend/TokenizeString",
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

    Only unary-request handlers are ever passed here: LoadModel and the four
    guarded RPCs all take a single request message.
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
                result = await original(request, context)
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
                async for response in original_stream(request, context):
                    yield response

            return _rebuild(handler, guard_stream)

        original_unary = handler.unary_unary

        async def guard(request, context):
            message = self.state.mismatch(getattr(request, "ModelIdentity", ""))
            if message is not None:
                await context.abort(grpc.StatusCode.NOT_FOUND, message)
            return await original_unary(request, context)

        return _rebuild(handler, guard)
