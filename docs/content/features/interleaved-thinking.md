
+++
disableToc = false
title = "Interleaved Thinking with Tool Calls"
weight = 17
url = "/features/interleaved-thinking/"
+++

Reasoning models can "think" before they answer. When such a model also calls a tool, the useful behaviour is for the thinking and the tool call to travel together, and for that thinking to survive the tool-result round trip. LocalAI calls this **interleaved thinking**: a single assistant turn carries both `reasoning` and `tool_calls`, and the client hands the `reasoning` back on the next turn so the model's chain of thought is not lost when the tool result is appended.

This matters because a tool-calling loop is multi-turn. The model reasons, asks for a tool, your client runs the tool, and then you call the model again with the tool result. Without interleaved thinking the reasoning produced in the first turn is discarded, and the model has to reconstruct its plan from scratch. With it, the reasoning is echoed back and the model continues where it left off.

See also: [OpenAI Functions and Tools]({{%relref "features/openai-functions" %}}) for how tool calls are extracted per backend, [Text Generation (GPT)]({{%relref "features/text-generation" %}}) for the chat completions basics, and [Advanced model configuration]({{%relref "advanced/model-configuration" %}}) for the model `options` referenced below.

## The round-trip contract

An assistant turn that both reasons and calls a tool returns the two fields side by side:

- `reasoning` holds the model's thinking.
- `tool_calls` holds the structured calls.
- `finish_reason` is `tool_calls`.

Your client runs the tool, then sends the conversation back with:

1. the original assistant message (including its `reasoning` and `tool_calls`), and
2. a `tool` role message carrying the tool result.

LocalAI reads the returned `reasoning` back into the model's context so the chain is continuous.

### Field naming

**OpenAI chat completions** (`/v1/chat/completions`): the response carries `reasoning` alongside `tool_calls`. On inbound assistant messages LocalAI now also accepts `reasoning_content` as an alias for `reasoning`. This alias exists because several clients (vLLM, DeepSeek, and cogito) emit the field under the name `reasoning_content`; either name is accepted and mapped to the same internal field.

**Anthropic Messages** (`/v1/messages`): reasoning is carried as `thinking` content blocks. On the local path LocalAI emits a `thinking` block before the `tool_use` block, and reads inbound `thinking` blocks back into reasoning. Because local models produce no cryptographic signature, LocalAI attaches a synthetic opaque `signature` to the emitted block, and does not validate the signature on inbound blocks. The `thinking` block is only emitted when the request opts in with the `thinking` parameter:

```json
{
  "model": "your-reasoning-model",
  "max_tokens": 1024,
  "thinking": { "type": "enabled" },
  "messages": [
    { "role": "user", "content": "What is the weather in Rome?" }
  ]
}
```

## Enabling reasoning per backend

Interleaved thinking requires the backend to separate the model's thinking from its final answer. How you turn that on depends on the backend.

### llama.cpp

Set the `reasoning_format` model option. Accepted values: `none`, `auto`, `deepseek`, `deepseek-legacy`. `auto` lets the backend pick based on the model's chat template; `deepseek` and `deepseek-legacy` force the DeepSeek-style `<think>...</think>` extraction.

```yaml
name: my-reasoning-model
backend: llama-cpp
parameters:
  model: my-reasoning-model.gguf
options:
  - reasoning_format:auto
```

### vLLM

Set the `reasoning_parser` model option to vLLM's native reasoning parser for the model family. LocalAI also ships an auto-configuration hook (`core/config/hooks_vllm.go`) that sets the reasoning parser and the tool-call parser together for known families, so for gallery-imported vLLM models this is often configured for you.

```yaml
name: my-vllm-model
backend: vllm
options:
  - reasoning_parser:deepseek_r1
  - tool_parser:hermes
```

### SGLang

Set both the `reasoning_parser` and the `tool_call_parser` model options.

```yaml
name: my-sglang-model
backend: sglang
options:
  - reasoning_parser:deepseek-r1
  - tool_call_parser:qwen25
```

## Worked example

Request a chat completion with a tool defined and a prompt that requires the model to reason before acting:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "your-reasoning-model",
  "messages": [
    { "role": "user", "content": "What is the weather in Rome?" }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get the current weather for a city",
        "parameters": {
          "type": "object",
          "properties": {
            "city": { "type": "string" }
          },
          "required": ["city"]
        }
      }
    }
  ]
}'
```

The response message carries both the reasoning and the tool call, and `finish_reason` is `tool_calls`:

```json
{
  "choices": [
    {
      "finish_reason": "tool_calls",
      "message": {
        "role": "assistant",
        "content": "",
        "reasoning": "Okay, the user is asking about the weather in Rome... I need to call get_weather with city Rome.",
        "tool_calls": [
          {
            "id": "call_abc123",
            "type": "function",
            "function": {
              "name": "get_weather",
              "arguments": "{\"city\":\"Rome\"}"
            }
          }
        ]
      }
    }
  ]
}
```

To continue, run `get_weather`, then send the conversation back with the assistant message (keeping its `reasoning` and `tool_calls`) followed by a `tool` message holding the result. LocalAI feeds the returned reasoning back into context so the model resumes its chain of thought.

## Known limitations

**Streaming Anthropic `thinking` blocks.** In streaming mode, a `thinking` block is currently emitted only on the tool-call path that goes through the llama.cpp C++ autoparser. A plain streaming text-only turn, or a tool turn resolved through the inline token path, does not stream a `thinking` block. The equivalent non-streaming request does return one. Non-streaming emits thinking on all branches; only the streaming path has this gap.

**Upstream llama.cpp leak on newest hybrid models.** On the newest hybrid reasoning models (Qwen3.5 / Qwen3.6) there is an upstream llama.cpp bug where tool calls can leak into `reasoning_content` instead of being parsed into `tool_calls`. This is tracked upstream at [ggml-org/llama.cpp discussion #23351](https://github.com/ggml-org/llama.cpp/discussions/23351).

**Reasoning budget vs the tool call.** If a reasoning model's thinking exhausts the output budget (`max_tokens`) before it emits the tool call, no tool call is produced. Give the model enough `max_tokens` to cover both the reasoning and the call. In this case LocalAI reports `finish_reason: "length"`.
