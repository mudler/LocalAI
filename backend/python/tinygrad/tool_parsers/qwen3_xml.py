"""Qwen 3 XML tool-call parser.

Qwen 3 Instruct emits tool calls wrapped in a two-level tag structure:

    <tool_call>
    <function=get_weather>
    <parameter=city>
    Paris
    </parameter>
    <parameter=unit>
    celsius
    </parameter>
    </function>
    </tool_call>

Parameter values are raw text — we treat them as strings unless they look
like JSON (in which case we try to parse so numbers / booleans round-trip
cleanly). Qwen 3 also supports `<think>...</think>` reasoning blocks before
the tool call — these are captured via the shared Hermes convention.
"""
from __future__ import annotations

import json
import re

from .base import ToolCall, ToolParser, register

_TOOL_CALL_RE = re.compile(r"<tool_call>(.*?)</tool_call>", re.DOTALL)
_FUNCTION_RE = re.compile(r"<function=([^>]+)>(.*?)</function>", re.DOTALL)
_PARAMETER_RE = re.compile(r"<parameter=([^>]+)>(.*?)</parameter>", re.DOTALL)
_THINK_RE = re.compile(r"<think>(.*?)</think>", re.DOTALL)


def _maybe_json(value: str):
    value = value.strip()
    if not value:
        return value
    if value[0] in "{[\"" or value in ("true", "false", "null") or value.lstrip("-").replace(".", "", 1).isdigit():
        try:
            return json.loads(value)
        except json.JSONDecodeError:
            return value
    return value


@register
class Qwen3XmlToolParser(ToolParser):
    name = "qwen3_xml"

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        # Strip reasoning blocks from the user-visible content.
        stripped = _THINK_RE.sub("", text)

        calls: list[ToolCall] = []
        for match in _TOOL_CALL_RE.finditer(stripped):
            body = match.group(1)
            fn_match = _FUNCTION_RE.search(body)
            if not fn_match:
                continue
            name = fn_match.group(1).strip()
            params_body = fn_match.group(2)

            params: dict[str, object] = {}
            for pm in _PARAMETER_RE.finditer(params_body):
                params[pm.group(1).strip()] = _maybe_json(pm.group(2))

            calls.append(ToolCall(
                index=len(calls),
                name=name,
                arguments=json.dumps(params, ensure_ascii=False),
            ))

        content = _TOOL_CALL_RE.sub("", stripped).strip()
        return content, calls
