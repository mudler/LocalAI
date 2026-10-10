"""Build backend_pb2.Reply messages from engine results."""
from __future__ import annotations

import backend_pb2

from tf_engine import Delta, Result


def stream_reply(delta: Delta) -> backend_pb2.Reply:
    return backend_pb2.Reply(
        message=delta.content.encode("utf-8"),
        chat_deltas=[backend_pb2.ChatDelta(content=delta.content, reasoning_content=delta.reasoning)],
    )


def final_reply(result: Result, *, with_text: bool) -> backend_pb2.Reply:
    """The closing reply of a call.

    A streamed call already sent the text, so it passes with_text=False and this
    reply carries only the tool calls and the token counts. A unary call passes
    with_text=True and gets the whole answer.
    """
    tool_calls = [
        backend_pb2.ToolCallDelta(index=c.index, id=c.id, name=c.name, arguments=c.arguments)
        for c in result.tool_calls
    ]
    text = result.content if with_text else ""
    reasoning = result.reasoning if with_text else ""
    return backend_pb2.Reply(
        message=text.encode("utf-8"),
        tokens=result.completion_tokens,
        prompt_tokens=result.prompt_tokens,
        chat_deltas=[
            backend_pb2.ChatDelta(content=text, reasoning_content=reasoning, tool_calls=tool_calls)
        ],
    )
