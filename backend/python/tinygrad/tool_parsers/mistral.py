"""Mistral / Mixtral tool-call parser.

Mistral Nemo / Small / Large Instruct emit tool calls prefixed with the
`[TOOL_CALLS]` control token, followed by a JSON array:

    [TOOL_CALLS][{"name": "get_weather", "arguments": {"city": "Paris"}}]

Multiple calls live inside the same array. Any text before `[TOOL_CALLS]` is
normal assistant content and should surface to the user.
"""
from __future__ import annotations

import json
import re

from .base import ToolCall, ToolParser, register

_MARKER = "[TOOL_CALLS]"
_JSON_ARRAY_RE = re.compile(r"\[\s*(?:\{.*?\}\s*,?\s*)+\]", re.DOTALL)


@register
class MistralToolParser(ToolParser):
    name = "mistral"

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        if _MARKER not in text:
            return text.strip(), []

        head, tail = text.split(_MARKER, 1)
        content = head.strip()

        match = _JSON_ARRAY_RE.search(tail)
        if not match:
            return content, []

        try:
            arr = json.loads(match.group(0))
        except json.JSONDecodeError:
            return content, []

        if not isinstance(arr, list):
            return content, []

        calls: list[ToolCall] = []
        for i, obj in enumerate(arr):
            if not isinstance(obj, dict):
                continue
            name = obj.get("name")
            if not isinstance(name, str):
                continue
            args = obj.get("arguments", {})
            args_str = args if isinstance(args, str) else json.dumps(args, ensure_ascii=False)
            calls.append(ToolCall(index=i, name=name, arguments=args_str))

        return content, calls
