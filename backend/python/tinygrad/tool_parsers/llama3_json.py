"""Llama 3.1 / 3.2 / 3.3 JSON tool-call parser.

Meta's Llama 3.1+ instruct chat templates emit tool calls in two broadly
compatible shapes:

  1. With the `<|python_tag|>` lead-in:
        <|python_tag|>{"name": "get_weather", "parameters": {"city": "Paris"}}
  2. As a bare JSON object (or list of objects) at the end of the turn.

We also handle multi-call shapes where the model emits several JSON objects
separated by `;` or newlines, and JSON arrays `[{...}, {...}]`. The key field
for Llama 3 is historically `parameters` (older docs) but recent checkpoints
also emit `arguments` — accept either.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass

from .base import ToolCall, ToolParser, register

_PYTHON_TAG = "<|python_tag|>"
_JSON_OBJECT_RE = re.compile(r"\{[^{}]*(?:\{[^{}]*\}[^{}]*)*\}", re.DOTALL)


def _coerce_call(obj: object, index: int) -> ToolCall | None:
    if not isinstance(obj, dict):
        return None
    name = obj.get("name")
    if not isinstance(name, str):
        return None
    args = obj.get("arguments", obj.get("parameters", {}))
    args_str = args if isinstance(args, str) else json.dumps(args, ensure_ascii=False)
    return ToolCall(index=index, name=name, arguments=args_str)


@register
class Llama3JsonToolParser(ToolParser):
    name = "llama3_json"

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        calls: list[ToolCall] = []

        # Strip <|python_tag|> segments first — each segment is one tool call
        # body. The content after the final python_tag (if any) is the call.
        remaining = text
        if _PYTHON_TAG in text:
            head, *tails = text.split(_PYTHON_TAG)
            remaining = head
            for tail in tails:
                parsed = _try_parse(tail.strip(), len(calls))
                calls.extend(parsed)

        # Any JSON objects / arrays left in `remaining` count as tool calls too
        # if they parse to a {"name": ..., "arguments": ...} shape.
        for match in _JSON_OBJECT_RE.finditer(remaining):
            parsed = _try_parse(match.group(0), len(calls))
            if parsed:
                calls.extend(parsed)
                remaining = remaining.replace(match.group(0), "", 1)

        content = remaining.strip()
        return content, calls


def _try_parse(blob: str, start_index: int) -> list[ToolCall]:
    """Parse a fragment that may be a JSON object or a JSON array of objects."""
    blob = blob.strip().rstrip(";")
    if not blob:
        return []
    try:
        obj = json.loads(blob)
    except json.JSONDecodeError:
        return []
    if isinstance(obj, dict):
        call = _coerce_call(obj, start_index)
        return [call] if call else []
    if isinstance(obj, list):
        calls: list[ToolCall] = []
        for i, item in enumerate(obj):
            c = _coerce_call(item, start_index + i)
            if c:
                calls.append(c)
        return calls
    return []
