
+++
disableToc = false
title = "GPT Vision"
weight = 14
url = "/features/gpt-vision/"
+++

LocalAI supports understanding images by using [LLaVA](https://llava.hliu.cc/), and implements the [GPT Vision API](https://platform.openai.com/docs/guides/vision) from OpenAI.

![llava](https://github.com/mudler/LocalAI/assets/2420543/cb0a0897-3b58-4350-af66-e6f4387b58d3)

## Usage

OpenAI docs: https://platform.openai.com/docs/guides/vision

First install a vision-capable model from the gallery (the examples below use `moondream2-20250414`, a small vision model):

```bash
local-ai run moondream2-20250414
```

To let LocalAI understand and reply with what sees in the image, use the `/v1/chat/completions` endpoint, for example with curl:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "moondream2-20250414",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "What is in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'
```

Grammars and function tools can be used as well in conjunction with vision APIs:

```bash
 curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "moondream2-20250414", "grammar": "root ::= (\"yes\" | \"no\")",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "Is there some grass in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'
```

### Setup

Install a vision-capable model from the gallery, either from the **Models** page in the web UI or from the CLI:

```bash
local-ai run moondream2-20250414
```

Other vision models are available in the gallery (for example `smolvlm-instruct` and `smolvlm2-2.2b-instruct`). Browse them on the **Models** page or see the {{% relref "features/model-gallery" %}}.