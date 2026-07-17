+++
disableToc = false
title = "Features"
weight = 4
icon = "lightbulb"
type = "chapter"
url = "/features/"
+++

LocalAI provides a comprehensive set of features for running AI models locally. The pages in this section are grouped by capability, and the left navigation is ordered to match these groups.

## Text

- **[Text Generation]({{% relref "features/text-generation" %}})** - Generate text with GPT-compatible models using various backends.
- **[OpenAI Functions and tools]({{% relref "features/openai-functions" %}})** - Use function calling and the tools API with local models.
- **[Constrained Grammars]({{% relref "features/constrained_grammars" %}})** - Control model output format with BNF grammars.
- **[Interleaved thinking]({{% relref "features/interleaved-thinking" %}})** - Reasoning models that interleave thought and output.
- **[Model aliases]({{% relref "features/model-aliases" %}})** - Expose one model under several names.
- **[API discovery]({{% relref "features/api-discovery" %}})** - How LocalAI advertises its capability surface.

## Agents

- **[Agents]({{% relref "features/agents" %}})** - Autonomous AI agents with tools, knowledge base, and skills.
- **[Agent Actions]({{% relref "features/agent-actions" %}})** - The catalog of actions an agent can run.
- **[Model Context Protocol (MCP)]({{% relref "features/mcp" %}})** - Give a model external tools over MCP.
- **[LocalAI Assistant]({{% relref "features/localai-assistant" %}})** - Chat to administer your LocalAI instance.

## Audio

- **[Audio to text]({{% relref "features/audio-to-text" %}})** - Transcribe audio to text.
- **[Text to audio]({{% relref "features/text-to-audio" %}})** - Generate speech, music, and sound effects from text.
- **[Audio classification]({{% relref "features/audio-classification" %}})** - Classify sounds and audio events.
- **[Audio diarization]({{% relref "features/audio-diarization" %}})** - Separate speakers in an audio stream.
- **[Audio transform]({{% relref "features/audio-transform" %}})** - Transform and process audio.
- **[Voice Activity Detection]({{% relref "features/voice-activity-detection" %}})** - Detect speech segments in audio data.
- **[Voice recognition]({{% relref "features/voice-recognition" %}})** - Identify speakers by voice.
- **[Realtime API]({{% relref "features/openai-realtime" %}})** - Low-latency multi-modal conversations (voice and text) over WebSocket.

## Vision

- **[GPT Vision]({{% relref "features/gpt-vision" %}})** - Analyze and understand images with vision-language models.
- **[Object detection]({{% relref "features/object-detection" %}})** - Detect and locate objects in images.
- **[Face recognition]({{% relref "features/face-recognition" %}})** - Recognize faces in images.

## Image and Video

- **[Image Generation]({{% relref "features/image-generation" %}})** - Create images with Stable Diffusion and other diffusion models.
- **[Video Generation]({{% relref "features/video-generation" %}})** - Generate videos from text, image, or audio conditioning, including LongCat and avatar workflows.

## Retrieval

- **[Embeddings]({{% relref "features/embeddings" %}})** - Generate vector embeddings for semantic search and RAG applications.
- **[Reranker]({{% relref "features/reranker" %}})** - Improve retrieval accuracy with cross-encoder models.
- **[Stores]({{% relref "features/stores" %}})** - Vector similarity search for embeddings.

## Distributed and acceleration

- **[Distributed inference]({{% relref "features/distributed_inferencing" %}})** - Scale inference across multiple nodes (P2P federation or production distributed mode).
- **[Distributed mode]({{% relref "features/distributed-mode" %}})** - The production distributed deployment.
- **[MLX distributed]({{% relref "features/mlx-distributed" %}})** - Distributed inference on Apple Silicon with MLX.
- **[GPU Acceleration]({{% relref "features/GPU-acceleration" %}})** - Optimize performance with GPU support.

## Platform and model management

- **[Backends]({{% relref "features/backends" %}})** - Available backends and how to manage them.
- **[Model Gallery]({{% relref "features/model-gallery" %}})** - Browse and install pre-configured models.
- **[Runtime Settings]({{% relref "features/runtime-settings" %}})** - Configure application settings via the web UI without restarting.
- **[Quantization]({{% relref "features/quantization" %}})** - Quantize models for smaller memory footprint.
- **[Fine-tuning]({{% relref "features/fine-tuning" %}})** - Fine-tune models on your own data.
- **[Authentication]({{% relref "features/authentication" %}})** - Protect the API with keys.

For operator-facing runtime, proxy, and monitoring concerns (middleware, cloud and MITM proxies, backend monitor), see the [Operations]({{% relref "operations" %}}) section.

## Getting Started

To start using these features, make sure you have [LocalAI installed]({{% relref "getting-started/install" %}}) and have [downloaded some models]({{% relref "getting-started/models" %}}). Then explore the feature pages above to learn how to use each capability.
