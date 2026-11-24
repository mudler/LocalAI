"""Common types + parser registry for tool-call extraction."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional


@dataclass
class ToolCall:
    """One extracted tool call — maps 1:1 to backend_pb2.ToolCallDelta."""
    index: int
    name: str
    arguments: str  # JSON string
    id: str = ""


class ToolParser:
    """Parser interface.

    Subclasses implement `parse` (full non-streaming pass) and optionally
    `parse_stream` (incremental). The default `parse_stream` buffers until a
    full response is available and then delegates to `parse`.
    """

    name: str = "base"

    def __init__(self) -> None:
        self._stream_buffer = ""
        self._stream_index = 0

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        """Return (content_for_user, tool_calls)."""
        raise NotImplementedError

    def parse_stream(self, delta: str, finished: bool = False) -> tuple[str, list[ToolCall]]:
        """Accumulate a streaming delta. Emits any tool calls that have closed.

        Default behavior: buffer until `finished=True`, then parse once.
        Subclasses can override to emit mid-stream.
        """
        self._stream_buffer += delta
        if not finished:
            return "", []
        content, calls = self.parse(self._stream_buffer)
        # Re-index starting from whatever we've already emitted in this stream.
        reindexed: list[ToolCall] = []
        for i, c in enumerate(calls):
            reindexed.append(ToolCall(
                index=self._stream_index + i,
                name=c.name,
                arguments=c.arguments,
                id=c.id,
            ))
        self._stream_index += len(reindexed)
        return content, reindexed

    def reset(self) -> None:
        self._stream_buffer = ""
        self._stream_index = 0


_REGISTRY: dict[str, type[ToolParser]] = {}


def register(cls: type[ToolParser]) -> type[ToolParser]:
    _REGISTRY[cls.name] = cls
    return cls


def resolve_parser(name: Optional[str]) -> ToolParser:
    """Return a parser instance by name, falling back to a no-op passthrough."""
    # Import for side effects — each module registers itself.
    from . import hermes, llama3_json, mistral, qwen3_xml  # noqa: F401

    if name and name in _REGISTRY:
        return _REGISTRY[name]()
    return PassthroughToolParser()


class PassthroughToolParser(ToolParser):
    """No-op parser — used when no tool_parser is configured."""
    name = "passthrough"

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        return text, []
