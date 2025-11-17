+++
disableToc = false
title = "Your First Chat with LocalAI"
weight = 1
icon = "chat"
description = "Get LocalAI running and have your first conversation in minutes"
+++

This tutorial will guide you through installing LocalAI and having your first conversation with an AI model. By the end, you'll have LocalAI running and be able to chat with a local AI model.

## Prerequisites

- A computer running Linux, macOS, or Windows (with WSL)
- At least 4GB of RAM (8GB+ recommended)
- Docker installed (optional, but recommended for easiest setup)

## Step 1: Install LocalAI

Choose the installation method that works best for you:

### Option A: Docker (Recommended for Beginners)

```bash
# Run LocalAI with AIO (All-in-One) image - includes pre-configured models
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu
```

This will:
- Download the LocalAI image
- Start the API server on port 8080
- Automatically download and configure models

### Option B: Quick Install Script (Linux)

```bash
curl https://localai.io/install.sh | sh
```

### Option C: macOS DMG

Download the DMG from [GitHub Releases](https://github.com/mudler/LocalAI/releases/latest/download/LocalAI.dmg) and install it.

For more installation options, see the [Quickstart Guide]({{% relref "docs/getting-started/quickstart" %}}).

## Step 2: Verify Installation

Once LocalAI is running, verify it's working:

```bash
# Check if the API is responding
curl http://localhost:8080/v1/models
```

You should see a JSON response listing available models. If using the AIO image, you'll see models like `gpt-4`, `gpt-4-vision-preview`, etc.

## Step 3: Access the WebUI

Open your web browser and navigate to:

```
http://localhost:8080
```

You'll see the LocalAI WebUI with:
- A chat interface
- Model gallery
- Backend management
- Configuration options

## Step 4: Your First Chat

### Using the WebUI

1. In the WebUI, you'll see a chat interface
2. Select a model from the dropdown (if multiple models are available)
3. Type your message and press Enter
4. Wait for the AI to respond!

### Using the API (Command Line)

You can also chat using curl:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {"role": "user", "content": "Hello! Can you introduce yourself?"}
    ],
    "temperature": 0.7
  }'
```

### Using Python

```python
import requests

response = requests.post(
    "http://localhost:8080/v1/chat/completions",
    json={
        "model": "gpt-4",
        "messages": [
            {"role": "user", "content": "Hello! Can you introduce yourself?"}
        ],
        "temperature": 0.7
    }
)

print(response.json()["choices"][0]["message"]["content"])
```

## Step 5: Try Different Models

If you're using the AIO image, you have several models pre-installed:

- `gpt-4` - Text generation
- `gpt-4-vision-preview` - Vision and text
- `tts-1` - Text to speech
- `whisper-1` - Speech to text

Try asking the vision model about an image, or generate speech with the TTS model!

### Installing New Models via WebUI

To install additional models, you can use the WebUI's import interface:

1. In the WebUI, navigate to the "Models" tab
2. Click "Import Model" or "New Model"
3. Enter a model URI (e.g., `huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`)
4. Configure preferences or use Advanced Mode for YAML editing
5. Click "Import Model" to start the installation

For more details, see [Setting Up Models]({{% relref "docs/tutorials/setting-up-models" %}}).

## Troubleshooting

### Port 8080 is already in use

Change the port mapping:
```bash
docker run -p 8081:8080 --name local-ai -ti localai/localai:latest-aio-cpu
```
Then access at `http://localhost:8081`

### No models available

If you're using a standard (non-AIO) image, you need to install models. See [Setting Up Models]({{% relref "docs/tutorials/setting-up-models" %}}) tutorial.

### Slow responses

- Check if you have enough RAM
- Consider using a smaller model
- Enable GPU acceleration (see [Using GPU]({{% relref "docs/tutorials/using-gpu" %}}))

## What's Next?

Congratulations! You've successfully set up LocalAI and had your first chat. Here's what to explore next:

1. **[Setting Up Models]({{% relref "docs/tutorials/setting-up-models" %}})** - Learn how to install and configure different models
2. **[Using GPU Acceleration]({{% relref "docs/tutorials/using-gpu" %}})** - Speed up inference with GPU support
3. **[Try It Out]({{% relref "docs/getting-started/try-it-out" %}})** - Explore more API endpoints and features
4. **[Features Documentation]({{% relref "docs/features" %}})** - Discover all LocalAI capabilities

## See Also

- [Quickstart Guide]({{% relref "docs/getting-started/quickstart" %}})
- [FAQ]({{% relref "docs/faq" %}})
- [Troubleshooting Guide]({{% relref "docs/troubleshooting" %}})

