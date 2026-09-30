"""Shared types for the tensorfold engine adapters."""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Callable, Protocol


@dataclass
class Delta:
    content: str = ""
    reasoning: str = ""


@dataclass
class ToolCall:
    index: int
    id: str
    name: str
    arguments: str


@dataclass
class Result:
    content: str = ""
    reasoning: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)
    finish_reason: str = "stop"
    prompt_tokens: int = 0
    completion_tokens: int = 0
    cached_tokens: int = 0


class GenerationCancelled(Exception):
    """The client went away; the adapter stopped the engine."""


class InvalidRequest(Exception):
    """The request itself is wrong (maps to INVALID_ARGUMENT), not the server."""


class Engine(Protocol):
    def generate(
        self,
        body: dict[str, Any],
        chat: bool,
        on_delta: Callable[[Delta], None] | None,
        is_cancelled: Callable[[], bool],
    ) -> Result: ...

    def tokenize(self, text: str) -> list[int]: ...

    def close(self) -> None: ...


def tool_calls_from_openai(calls: list[dict[str, Any]] | None) -> list[ToolCall]:
    out: list[ToolCall] = []
    for index, call in enumerate(calls or []):
        function = call.get("function") or {}
        arguments = function.get("arguments", "")
        if not isinstance(arguments, str):
            arguments = json.dumps(arguments)
        out.append(
            ToolCall(
                index=index,
                id=str(call.get("id") or f"call_{index}"),
                name=str(function.get("name") or ""),
                arguments=arguments,
            )
        )
    return out
