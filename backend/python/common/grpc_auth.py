"""Shared gRPC bearer token authentication interceptor for LocalAI Python backends.

When the environment variable LOCALAI_GRPC_AUTH_TOKEN is set, requests without
a valid Bearer token in the 'authorization' metadata header are rejected with
UNAUTHENTICATED. When the variable is empty or unset, no authentication is
performed (backward compatible).
"""

import hmac
import os

import grpc

from model_identity import AsyncModelIdentityInterceptor, ModelIdentityInterceptor
from parent_watch import start_parent_death_watcher


class _AbortHandler(grpc.RpcMethodHandler):
    """A method handler that immediately aborts with UNAUTHENTICATED."""

    def __init__(self):
        self.request_streaming = False
        self.response_streaming = False
        self.request_deserializer = None
        self.response_serializer = None
        self.unary_unary = self._abort
        self.unary_stream = None
        self.stream_unary = None
        self.stream_stream = None

    @staticmethod
    def _abort(request, context):
        context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid token")


class TokenAuthInterceptor(grpc.ServerInterceptor):
    """Sync gRPC server interceptor that validates a bearer token."""

    def __init__(self, token: str):
        self._token = token
        self._abort_handler = _AbortHandler()

    def intercept_service(self, continuation, handler_call_details):
        metadata = dict(handler_call_details.invocation_metadata)
        auth = metadata.get("authorization", "")
        expected = "Bearer " + self._token
        if not hmac.compare_digest(auth, expected):
            return self._abort_handler
        return continuation(handler_call_details)


class AsyncTokenAuthInterceptor(grpc.aio.ServerInterceptor):
    """Async gRPC server interceptor that validates a bearer token."""

    def __init__(self, token: str):
        self._token = token

    async def intercept_service(self, continuation, handler_call_details):
        metadata = dict(handler_call_details.invocation_metadata)
        auth = metadata.get("authorization", "")
        expected = "Bearer " + self._token
        if not hmac.compare_digest(auth, expected):
            return _AbortHandler()
        return await continuation(handler_call_details)


def get_auth_interceptors(*, aio: bool = False):
    """Return the gRPC server interceptors every LocalAI Python backend installs.

    Always includes model-identity enforcement (model_identity.py), which is
    unrelated to authentication. Bearer token auth is added on top only when
    LOCALAI_GRPC_AUTH_TOKEN is set.

    Args:
        aio: If True, return async-compatible interceptors for grpc.aio.server().
             If False (default), return sync interceptors for grpc.server().
    """
    # Arm the best-effort parent-death backstop here: this is the single helper
    # every LocalAI Python backend invokes exactly once while building its gRPC
    # server (mirroring how the Go watcher arms in pkg/grpc's shared serve path).
    # start_parent_death_watcher() is idempotent and a no-op when disabled or on
    # unsupported platforms — see parent_watch.py.
    start_parent_death_watcher()

    # Model-identity enforcement is independent of authentication and must be
    # installed BEFORE the token check returns. gRPC auth is off by default, so
    # an identity interceptor added below the early return would never be
    # installed on any Python backend, and nothing would report it.
    interceptors = [AsyncModelIdentityInterceptor()] if aio else [ModelIdentityInterceptor()]

    token = os.environ.get("LOCALAI_GRPC_AUTH_TOKEN", "")
    if not token:
        return interceptors
    if aio:
        interceptors.append(AsyncTokenAuthInterceptor(token))
    else:
        interceptors.append(TokenAuthInterceptor(token))
    return interceptors
