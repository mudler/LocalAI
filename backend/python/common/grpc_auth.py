"""Shared gRPC bearer token authentication interceptor for LocalAI Python backends.

When the environment variable LOCALAI_GRPC_AUTH_TOKEN is set, requests without
a valid Bearer token in the 'authorization' metadata header are rejected with
UNAUTHENTICATED. When the variable is empty or unset, no authentication is
performed (backward compatible).
"""

import hmac
import os

import grpc


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
    """Return a list of gRPC interceptors for bearer token auth.

    Args:
        aio: If True, return async-compatible interceptors for grpc.aio.server().
             If False (default), return sync interceptors for grpc.server().

    Returns an empty list when LOCALAI_GRPC_AUTH_TOKEN is not set.
    """
    token = os.environ.get("LOCALAI_GRPC_AUTH_TOKEN", "")
    if not token:
        return []
    if aio:
        return [AsyncTokenAuthInterceptor(token)]
    return [TokenAuthInterceptor(token)]
