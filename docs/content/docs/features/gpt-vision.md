
+++
disableToc = false
title = "🆕 GPT Vision"
weight = 14
url = "/features/gpt-vision/"
+++

LocalAI supports understanding images by using [LLaVA](https://llava.hliu.cc/), and implements the [GPT Vision API](https://platform.openai.com/docs/guides/vision) from OpenAI.

![llava](https://github.com/mudler/LocalAI/assets/2420543/cb0a0897-3b58-4350-af66-e6f4387b58d3)

## Usage

OpenAI docs: https://platform.openai.com/docs/guides/vision

To let LocalAI understand and reply with what sees in the image, use the `/v1/chat/completions` endpoint, for example with curl:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llava",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "What is in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'
```

### Setup

To setup the LLaVa models, follow the full example in the [configuration examples](https://github.com/mudler/LocalAI/blob/master/examples/configurations/README.md#llava).
