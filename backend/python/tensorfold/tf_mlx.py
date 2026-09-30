"""MLX adapter: drives a captured tensorfold ChatApp.

ChatApp.chat is the whole engine, but the request normalization that turns an
OpenAI body into its arguments lives in upstream's HTTP handler
(tensorfold/server/http.py, do_POST). This module reproduces exactly that
sequence from the same helper functions, so it drifts only if the sequence
itself changes. tests/test_embed_surface.py pins the helper names.
"""
from __future__ import annotations

import types
from typing import Any, Callable

from tf_engine import Delta, GenerationCancelled, InvalidRequest, Result, tool_calls_from_openai

# Request fields the HTTP handler copies into ChatApp's `sampling` argument.
_SAMPLING_FIELDS = ("temperature", "top_p", "top_k", "min_p", "seed", "priority",
                    "draft", "thinking_budget", "ignore_eos", "stop")


def import_helpers() -> Any:
    from tensorfold.engine import grammar
    from tensorfold.server.cancellation import Cancellation, RequestCancelled
    from tensorfold.server.errors import RequestError
    from tensorfold.server.messages import normalize_messages, validate_modalities
    from tensorfold.server.request_options import parse_numbers, thinking_fields
    from tensorfold.server.tool_policy import ToolCallPolicy
    from tensorfold.server.tools import (
        active_tool_specs,
        parse_tool_calls_from_content,
        tool_choice_requires_call,
    )

    return types.SimpleNamespace(
        parse_numbers=parse_numbers, validate_modalities=validate_modalities,
        normalize_messages=normalize_messages, active_tool_specs=active_tool_specs,
        tool_choice_requires_call=tool_choice_requires_call, thinking_fields=thinking_fields,
        parse_tool_calls_from_content=parse_tool_calls_from_content, ToolCallPolicy=ToolCallPolicy,
        Cancellation=Cancellation, RequestCancelled=RequestCancelled, RequestError=RequestError,
        grammar=grammar,
    )


class MlxEngine:
    def __init__(self, app: Any, helpers: Any):
        self._app = app
        self._h = helpers

    def generate(self, body: dict[str, Any], chat: bool,
                 on_delta: Callable[[Delta], None] | None,
                 is_cancelled: Callable[[], bool]) -> Result:
        h, app = self._h, self._app
        cancellation = h.Cancellation()
        try:
            body = h.parse_numbers(body)
            h.validate_modalities(body)
            raw_kw: dict[str, Any] = {}
            if chat:
                messages = h.normalize_messages(
                    body.get("messages"), allow_images=getattr(app, "vision", None) is not None)
                tools = h.active_tool_specs(body.get("tools"), body.get("tool_choice"))
            else:
                messages, tools = [], []
                raw_kw["prompt"] = body.get("prompt", "")

            fields = {k: body[k] for k in (*_SAMPLING_FIELDS, *h.grammar.FIELDS) if k in body}
            problem = h.grammar.refusal(body, app)
            if problem:
                raise InvalidRequest(problem)
            if tools and h.tool_choice_requires_call(body.get("tool_choice")):
                fields["tool_call_required"] = True
            fields.update(h.thinking_fields(body, getattr(app, "effort_levels", frozenset())))

            kw: dict[str, Any] = {}
            if getattr(app, "accepts_sampling", False):
                kw["sampling"] = fields
            if getattr(app, "accepts_cancellation", False):
                kw["cancellation"] = cancellation

            policy = h.ToolCallPolicy(body)
            streamed = [False]

            def poll() -> None:
                # ChatApp checks its Cancellation between rounds; the gRPC
                # context only tells us the client left, so relay it.
                if is_cancelled():
                    cancellation.cancel()

            def deliver(delta: Any) -> None:
                poll()
                if on_delta is None:
                    return
                if isinstance(delta, str):
                    if delta:
                        on_delta(Delta(content=delta))
                elif isinstance(delta, dict):
                    reasoning = delta.get("reasoning_content") or ""
                    content = delta.get("content") or ""
                    if reasoning or content:
                        on_delta(Delta(content=content, reasoning=reasoning))

            def prose(delta: Any) -> None:
                delta = policy.delta(delta)
                if not delta:
                    return
                streamed[0] = True
                deliver(delta)

            if tools:
                if getattr(app, "streams_prose_with_tools", False):
                    kw["on_delta"] = prose
                kw["tools"] = tools
            else:
                kw["on_delta"] = deliver

            reply = app.chat(
                messages,
                max_tokens=body.get("max_tokens"),
                temperature=float(body.get("temperature") or 0.0),
                **kw, **raw_kw,
            )
            if tools:
                reply = policy.finish(reply, tools, h.parse_tool_calls_from_content)
                tail = policy.flush()
                if tail:
                    streamed[0] = True
                    deliver(tail)
                if not reply.get("tool_calls") and reply.get("content") and not streamed[0]:
                    deliver(str(reply["content"]))
        except InvalidRequest:
            raise
        except h.RequestCancelled as exc:
            raise GenerationCancelled(str(exc)) from exc
        except h.RequestError as exc:
            raise InvalidRequest(str(exc)) from exc

        return Result(
            content=str(reply.get("content") or ""),
            reasoning=str(reply.get("reasoning") or ""),
            tool_calls=tool_calls_from_openai(reply.get("tool_calls")),
            finish_reason=str(reply.get("finish_reason") or "stop"),
            prompt_tokens=int(reply.get("prompt_tokens") or 0),
            completion_tokens=int(reply.get("completion_tokens") or 0),
            cached_tokens=int(reply.get("cached_tokens") or 0),
        )

    def tokenize(self, text: str) -> list[int]:
        encoded = self._app.tokenizer.encode(text)
        return list(encoded.tolist() if hasattr(encoded, "tolist") else encoded)

    def close(self) -> None:
        closer = getattr(self._app, "close", None)
        if closer is not None:
            closer()
        self._app = None
