"""Hermes-format tool-call parser.

Hermes 2 / 2.5 / 3 (and Qwen 2.5 Instruct, which adopted the same convention)
emit tool calls wrapped in `<tool_call>...</tool_call>` tags, where the inner
content is a JSON object with `name` and `arguments` keys:

    <tool_call>
    {"name": "get_weather", "arguments": {"city": "Paris"}}
    </tool_call>

Multiple tool calls may appear back-to-back. Text outside the tags is plain
assistant content that should surface to the user.

This parser also strips `<think>...</think>` reasoning blocks and returns them
via the reasoning_content channel (Qwen 3, DeepSeek-R1 distills).
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass

from .base import ToolCall, ToolParser, register

_TOOL_CALL_RE = re.compile(r"<tool_call>\s*(\{.*?\})\s*</tool_call>", re.DOTALL)
_THINK_RE = re.compile(r"<think>(.*?)</think>", re.DOTALL)


@dataclass
class HermesParseResult:
    content: str
    reasoning: str
    tool_calls: list[ToolCall]


@register
class HermesToolParser(ToolParser):
    name = "hermes"

    def _parse_full(self, text: str) -> HermesParseResult:
        reasoning_parts: list[str] = []

        def _capture_reasoning(match: re.Match[str]) -> str:
            reasoning_parts.append(match.group(1).strip())
            return ""

        text_wo_think = _THINK_RE.sub(_capture_reasoning, text)

        calls: list[ToolCall] = []
        for idx, match in enumerate(_TOOL_CALL_RE.finditer(text_wo_think)):
            raw = match.group(1)
            try:
                obj = json.loads(raw)
            except json.JSONDecodeError:
                continue
            if not isinstance(obj, dict):
                continue
            name = obj.get("name")
            if not isinstance(name, str):
                continue
            args = obj.get("arguments", {})
            args_str = args if isinstance(args, str) else json.dumps(args, ensure_ascii=False)
            calls.append(ToolCall(index=idx, name=name, arguments=args_str))

        content = _TOOL_CALL_RE.sub("", text_wo_think).strip()
        reasoning = "\n\n".join(reasoning_parts).strip()
        return HermesParseResult(content=content, reasoning=reasoning, tool_calls=calls)

    def parse(self, text: str) -> tuple[str, list[ToolCall]]:
        result = self._parse_full(text)
        return result.content, result.tool_calls

    def parse_full(self, text: str) -> HermesParseResult:
        return self._parse_full(text)
