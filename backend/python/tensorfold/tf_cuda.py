"""CUDA adapter: drives a captured tensorfold.cuda.server.App.

App.run already takes an OpenAI-shaped body, does its own normalization and
returns a plain dict, so this adapter only relays deltas, cancellation and the
result. Its `emit` callback returning False is how the engine is told to stop.
"""
from __future__ import annotations

import types
from typing import Any, Callable

from tf_engine import Delta, GenerationCancelled, InvalidRequest, Result, tool_calls_from_openai


def import_helpers() -> Any:
    from tensorfold.server.cancellation import RequestCancelled
    from tensorfold.server.errors import RequestError

    return types.SimpleNamespace(RequestError=RequestError, RequestCancelled=RequestCancelled)


def _delta_from(fields: dict[str, Any]) -> Delta | None:
    content = fields.get("content") or ""
    reasoning = fields.get("reasoning_content") or ""
    return Delta(content=content, reasoning=reasoning) if content or reasoning else None


class CudaEngine:
    def __init__(self, app: Any, helpers: Any):
        self._app = app
        self._h = helpers

    def generate(self, body: dict[str, Any], chat: bool,
                 on_delta: Callable[[Delta], None] | None,
                 is_cancelled: Callable[[], bool]) -> Result:
        def emit(fields: dict[str, Any]) -> bool:
            delta = _delta_from(fields)
            if delta is not None and on_delta is not None:
                on_delta(delta)
            return not is_cancelled()

        try:
            prepared = self._app.prepare(body, chat)
            result = self._app.run(body, chat, emit, prepared=prepared, cancelled=is_cancelled)
        except self._h.RequestCancelled as exc:
            raise GenerationCancelled(str(exc)) from exc
        except self._h.RequestError as exc:
            raise InvalidRequest(str(exc)) from exc

        # Text that only exists once the reply is complete (the part of a
        # tool-call turn the streaming filter held back).
        tail = _delta_from(result.get("final") or {})
        if tail is not None and on_delta is not None:
            on_delta(tail)

        return Result(
            content=str(result.get("content") or ""),
            reasoning=str(result.get("reasoning") or ""),
            tool_calls=tool_calls_from_openai(result.get("calls")),
            finish_reason=str(result.get("finish") or "stop"),
            prompt_tokens=int(result.get("prompt_tokens") or 0),
            completion_tokens=int(result.get("completion_tokens") or 0),
            cached_tokens=int(result.get("cached_tokens") or 0),
        )

    def tokenize(self, text: str) -> list[int]:
        return list(self._app.tok.encode(text).ids)

    def close(self) -> None:
        closer = getattr(getattr(self._app, "engine", None), "close", None)
        if closer is not None:
            closer()
        self._app = None
