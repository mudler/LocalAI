+++
title = "RKLLM on Rockchip NPUs"
weight = 37
toc = true
url = "/features/rkllm/"
description = "Serve RKLLM models from Rockchip NPUs through LocalAI"
tags = ["LLM", "Rockchip", "NPU", "RKLLM"]
categories = ["Features"]
+++

[RKLLM](https://github.com/airockchip/rknn-llm) runs converted language
models on supported Rockchip NPUs. Its server already exposes an
OpenAI-compatible chat-completions API, so LocalAI can put its authentication,
routing, usage tracking, and web UI in front of the board with the
`cloud-proxy` backend.

This integration does not install or convert RKLLM models. Model conversion is
a separate host-side step using RKLLM-Toolkit, and the resulting `.rkllm` file
must match the target SoC.

## Requirements

- A Linux Rockchip board supported by the installed RKLLM runtime. Upstream
  RKLLM 1.3.0 accepts `rk3588`, `rk3576`, `rv1126b`, and `rk3562` in its server
  demo.
- A model converted to `.rkllm` for that target.
- The upstream RKLLM Flask server running on the board and reachable from
  LocalAI.

RK3566 boards, including Quartz64 models with that SoC, are not in the current
upstream RKLLM server target list. LocalAI cannot add support for a SoC that the
RKLLM runtime does not support.

## Start the RKLLM server

Follow the upstream
[`rkllm_server_demo`](https://github.com/airockchip/rknn-llm/tree/main/examples/rkllm_server_demo)
instructions to deploy the runtime library, server, and converted model to the
board. For example, from an RKLLM checkout on the host:

```bash
cd examples/rkllm_server_demo
./build_rkllm_server_flask.sh \
  --workshop /userdata/rkllm-server \
  --model_path /userdata/models/qwen3.rkllm \
  --platform rk3588 \
  --adb_device YOUR_DEVICE_SERIAL
```

The helper starts the server on port `8080`. Confirm it is reachable before
configuring LocalAI:

```bash
curl http://ROCKCHIP_BOARD_IP:8080/v1/models
```

## Configure LocalAI

Create `models/rkllm.yaml`:

```yaml
name: rockchip-rkllm
backend: cloud-proxy

proxy:
  mode: passthrough
  provider: openai
  upstream_url: http://ROCKCHIP_BOARD_IP:8080/v1/chat/completions
  upstream_model: rkllm
  request_timeout_seconds: 600

# The upstream is on the local network, so cloud-egress PII filtering is not
# enabled by default in this example. Enable it if your deployment needs it.
pii:
  enabled: false
```

No API key setting is required by the upstream demo server. If you expose the
board beyond a trusted network, put an authenticated reverse proxy in front of
it and configure the corresponding key through `api_key_env` or `api_key_file`.

Start LocalAI, then use the local model name with any OpenAI-compatible client:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "rockchip-rkllm",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

LocalAI forwards the request and the streaming response without translating
the wire format. Sampling fields supported by the upstream server, including
`temperature`, `top_p`, `top_k`, `max_tokens`, `repeat_penalty`, and
`enable_thinking`, pass through unchanged.

## Limitations

- The upstream demo serves chat completions and model listing only. Embeddings,
  image generation, audio APIs, and legacy text completions are not available.
- Model conversion and runtime installation remain upstream RKLLM operations.
- Upstream serializes inference for a loaded model and returns HTTP 503 while
  it is busy. Scale with multiple boards and LocalAI routing if concurrent
  inference is required.
- The upstream demo returns model-generated tool calls as markup in
  `message.content`; passthrough mode does not convert that markup into OpenAI
  `message.tool_calls`. Clients that use tools must parse the model-specific
  format or put an adapter in front of the RKLLM server.

See [Cloud passthrough proxy]({{% relref "operations/cloud-proxy" %}}) for the
full proxy configuration, authentication, routing, and PII options.
