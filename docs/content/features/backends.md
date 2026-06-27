---
title: "Backends"
description: "Learn how to use, manage, and develop backends in LocalAI"
weight: 4
url: "/backends/"
---


LocalAI supports a variety of backends that can be used to run different types of AI models. There are core Backends which are included, and there are containerized applications that provide the runtime environment for specific model types, such as LLMs, diffusion models, or text-to-speech models.

## Available Backends

LocalAI ships **60+ backends** covering text generation, speech-to-text, text-to-speech, music and sound generation, image and video generation, vision and object detection, audio processing, reranking, fine-tuning, and more. Each one is published as an on-demand OCI image with the appropriate acceleration variants (CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T).

For the complete list of backends, the model families they support, and their acceleration targets, see the [Backend & Model Compatibility Table]({{%relref "reference/compatibility-table" %}}). The authoritative source is [`backend/index.yaml`](https://github.com/mudler/LocalAI/blob/master/backend/index.yaml), and the same catalog is browsable in the web UI under the **Backends** section.

## Managing Backends in the UI

The LocalAI web interface provides an intuitive way to manage your backends:

1. Navigate to the "Backends" section in the navigation menu
2. Browse available backends from configured galleries
3. Use the search bar to find specific backends by name, description, or type
4. Filter backends by type using the quick filter buttons (LLM, Diffusion, TTS, Whisper)
5. Install or delete backends with a single click
6. Monitor installation progress in real-time

Each backend card displays:
- Backend name and description
- Type of models it supports
- Installation status
- Action buttons (Install/Delete)
- Additional information via the info button

## Backend Galleries

Backend galleries are repositories that contain backend definitions. They work similarly to model galleries but are specifically for backends.

### Adding a Backend Gallery

You can add backend galleries by specifying the **Environment Variable**  `LOCALAI_BACKEND_GALLERIES`:

```bash
export LOCALAI_BACKEND_GALLERIES='[{"name":"my-gallery","url":"https://raw.githubusercontent.com/username/repo/main/backends"}]'
```
The URL needs to point to a valid yaml file, for example:

```yaml
- name: "test-backend"
  uri: "quay.io/image/tests:localai-backend-test"
  alias: "foo-backend"
```

Where URI is the path to an OCI container image.

### Backend Gallery Structure

A backend gallery is a collection of YAML files, each defining a backend. Here's an example structure:

```yaml
name: "llm-backend"
description: "A backend for running LLM models"
uri: "quay.io/username/llm-backend:latest"
alias: "llm"
tags:
  - "llm"
  - "text-generation"
```

## Pre-installing Backends

You can pre-install backends when starting LocalAI using the `LOCALAI_EXTERNAL_BACKENDS` environment variable:

```bash
export LOCALAI_EXTERNAL_BACKENDS="llm-backend,diffusion-backend"
local-ai run
```

## Creating a Backend

To create a new backend, you need to:

1. Create a container image that implements the LocalAI backend interface
2. Define a backend YAML file
3. Publish your backend to a container registry

### Backend Container Requirements

Your backend container should:

1. Implement the LocalAI backend interface (gRPC or HTTP)
2. Handle model loading and inference
3. Support the required model types
4. Include necessary dependencies
5. Have a top level `run.sh` file that will be used to run the backend
6. Pushed to a registry so can be used in a gallery

### Getting started

For getting started, see the available backends in LocalAI here: https://github.com/mudler/LocalAI/tree/master/backend . 

- For Python based backends there is a template that can be used as starting point: https://github.com/mudler/LocalAI/tree/master/backend/python/common/template . 
- For Golang based backends, you can see the `piper` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/go/piper
- For C++ based backends, you can see the `llama-cpp` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/cpp/llama-cpp

### Publishing Your Backend

1. Build your container image:
   ```bash
   docker build -t quay.io/username/my-backend:latest .
   ```

2. Push to a container registry:
   ```bash
   docker push quay.io/username/my-backend:latest
   ```

3. Add your backend to a gallery:
   - Create a YAML entry in your gallery repository
   - Include the backend definition
   - Make the gallery accessible via HTTP/HTTPS

## Backend Types

LocalAI supports various types of backends:

- **LLM Backends**: For running language models (e.g., llama.cpp, vLLM, SGLang, transformers, MLX)
  - **`llama-cpp-localai-paged`**: LocalAI's paged-attention llama.cpp variant - on-demand paged KV cache plus a decode-first prefill budget, tuned for NVFP4 dense/MoE on Blackwell/GB10. Same upstream llama.cpp pin as the stock `llama-cpp` backend, reusing its gRPC server; the paged engine is enabled per-model via the `paged_kv` / `max_batch_tokens` options. For Qwen3.5 gated-DeltaNet (hybrid SSM) models you can additionally set `options: [ssm_bf16_tau:<tokens>]` to enable the reduced-precision hybrid SSM-state fast mode: fast-decaying recurrent heads (memory length tau below the threshold, e.g. `32` / `64`) persist their state as bf16, halving that head's decode byte stream. Default off (`0`) keeps every head f32 and is bit-exact; when enabled the mode is **not** bit-exact (~91% same-top-p ceiling - see `backend/cpp/llama-cpp-localai-paged/patches/paged/README.md` for the quality/throughput profile).
- **Speech-to-Text Backends**: For transcription (e.g., whisper.cpp, parakeet.cpp, faster-whisper, NeMo)
- **Text-to-Speech Backends**: For speech synthesis (e.g., piper, Kokoro, VibeVoice, Qwen3-TTS)
- **Sound Generation Backends**: For music and audio generation (e.g., ACE-Step)
- **Sound Classification Backends**: For sound-event classification / audio tagging - identifying everyday sounds like baby cry, glass breaking, alarms (e.g., ced.cpp)
- **Image & Video Generation Backends**: For diffusion models (e.g., stable-diffusion.cpp, diffusers)
- **Vision & Detection Backends**: For object detection, segmentation, depth, and face/voice recognition (e.g., rf-detr.cpp, locate-anything.cpp, sam3.cpp, insightface)
- **Audio Processing Backends**: For voice activity detection and audio enhancement (e.g., Silero VAD, LocalVQE)
- **Utility Backends**: For reranking, PII/NER token classification, fine-tuning, quantization, and vector storage (e.g., rerankers, privacy-filter.cpp, TRL, local-store)

See the [Backend & Model Compatibility Table]({{%relref "reference/compatibility-table" %}}) for the full catalog.