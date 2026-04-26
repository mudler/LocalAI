"""Shared utilities for the mlx and mlx-vlm gRPC backends.

These helpers wrap mlx-lm's and mlx-vlm's native tool-parser modules, which
auto-detect the right parser from the model's chat template. Each tool
module exposes ``tool_call_start``, ``tool_call_end`` and
``parse_tool_call(text, tools) -> dict | list[dict]``.

The split-reasoning helper is generic enough to work with any think-start /
think-end delimiter pair.
"""
import json
import re
import sys
import uuid


def split_reasoning(text, think_start, think_end):
    """Split ``<think>...</think>`` blocks out of ``text``.

    Returns ``(reasoning_content, remaining_text)``. When ``think_start`` is
    empty or not found, returns ``("", text)`` unchanged.
    """
    if not think_start or not text or think_start not in text:
        return "", text
    pattern = re.compile(
        re.escape(think_start) + r"(.*?)" + re.escape(think_end or ""),
        re.DOTALL,
    )
    reasoning_parts = pattern.findall(text)
    if not reasoning_parts:
        return "", text
    remaining = pattern.sub("", text).strip()
    return "\n".join(p.strip() for p in reasoning_parts), remaining


def parse_tool_calls(text, tool_module, tools):
    """Extract tool calls from ``text`` using a mlx-lm tool module.

    Ports the ``process_tool_calls`` logic from
    ``mlx_vlm/server.py`` (v0.10 onwards). ``tool_module`` must expose
    ``tool_call_start``, ``tool_call_end`` and ``parse_tool_call``.

    Returns ``(calls, remaining_text)`` where ``calls`` is a list of dicts:

        [{"index": int, "id": str, "name": str, "arguments": str (JSON)}]

    and ``remaining_text`` is the free-form text with the tool call blocks
    removed. ``(calls, text)`` is returned unchanged if ``tool_module`` is
    ``None`` or the start delimiter isn't present.
    """
    if tool_module is None or not text:
        return [], text
    start = getattr(tool_module, "tool_call_start", None)
    end = getattr(tool_module, "tool_call_end", None)
    parse_fn = getattr(tool_module, "parse_tool_call", None)
    if not start or parse_fn is None or start not in text:
        return [], text

    if end == "" or end is None:
        pattern = re.compile(
            re.escape(start) + r".*?(?:\n|$)",
            re.DOTALL,
        )
    else:
        pattern = re.compile(
            re.escape(start) + r".*?" + re.escape(end),
            re.DOTALL,
        )

    matches = pattern.findall(text)
    if not matches:
        return [], text

    remaining = pattern.sub(" ", text).strip()
    calls = []
    for match in matches:
        call_body = match.strip().removeprefix(start)
        if end:
            call_body = call_body.removesuffix(end)
        call_body = call_body.strip()
        try:
            parsed = parse_fn(call_body, tools)
        except Exception as e:
            print(
                f"[mlx_utils] Invalid tool call: {call_body!r} ({e})",
                file=sys.stderr,
            )
            continue
        if not isinstance(parsed, list):
            parsed = [parsed]
        for tc in parsed:
            calls.append(
                {
                    "index": len(calls),
                    "id": str(uuid.uuid4()),
                    "name": (tc.get("name") or "").strip(),
                    "arguments": json.dumps(tc.get("arguments", {}), ensure_ascii=False),
                }
            )
    return calls, remaining
