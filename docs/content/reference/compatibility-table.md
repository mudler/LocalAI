
+++
disableToc = false
title = "Model compatibility table"
weight = 24
url = "/model-compatibility/"
+++

Besides llama based models, LocalAI is compatible also with other architectures. The table below lists all the backends, compatible models families and the associated repository.

{{% notice note %}}

LocalAI will attempt to automatically load models which are not explicitly configured for a specific backend. You can specify the backend to use by configuring a model with a YAML file. See [the advanced section]({{%relref "advanced" %}}) for more details.

 {{% /notice %}}

## Text Generation & Language Models

| Backend | Description | Capability | Embeddings | Streaming | Acceleration |
|---------|-------------|------------|------------|-----------|-------------|
| [llama.cpp](https://github.com/ggerganov/llama.cpp) | LLM inference in C/C++. Supports LLaMA, Mamba, RWKV, Falcon, Starcoder, GPT-2, [and many others](https://github.com/ggerganov/llama.cpp?tab=readme-ov-file#description) | GPT, Functions | yes | yes | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [vLLM](https://github.com/vllm-project/vllm) | Fast LLM serving with PagedAttention | GPT | no | no | CUDA 12, ROCm, Intel |
| [vLLM Omni](https://github.com/vllm-project/vllm) | Unified multimodal generation (text, image, video, audio) | Multimodal GPT | no | no | CUDA 12, ROCm |
| [transformers](https://github.com/huggingface/transformers) | HuggingFace Transformers framework | GPT, Embeddings, Multimodal | yes | yes* | CPU, CUDA 12/13, ROCm, Intel, Metal |
| [MLX](https://github.com/ml-explore/mlx-lm) | Apple Silicon LLM inference | GPT | no | no | Metal |
| [MLX-VLM](https://github.com/Blaizzy/mlx-vlm) | Vision-Language Models on Apple Silicon | Multimodal GPT | no | no | Metal |
| [MLX Distributed](https://github.com/ml-explore/mlx-lm) | Distributed LLM inference across multiple Apple Silicon Macs | GPT | no | no | Metal |

## Speech-to-Text

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [whisper.cpp](https://github.com/ggml-org/whisper.cpp) | OpenAI Whisper in C/C++ | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [faster-whisper](https://github.com/SYSTRAN/faster-whisper) | Fast Whisper with CTranslate2 | CUDA 12/13, ROCm, Intel, Metal |
| [WhisperX](https://github.com/m-bain/whisperX) | Word-level timestamps and speaker diarization | CPU, CUDA 12/13, ROCm, Metal |
| [moonshine](https://github.com/moonshine-ai/moonshine) | Ultra-fast transcription for low-end devices | CPU, CUDA 12/13, Metal |
| [voxtral](https://github.com/mudler/voxtral.c) | Voxtral Realtime 4B speech-to-text in C | CPU, Metal |
| [Qwen3-ASR](https://github.com/QwenLM/Qwen3-ASR) | Qwen3 automatic speech recognition | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [NeMo](https://github.com/NVIDIA/NeMo) | NVIDIA NeMo ASR toolkit | CPU, CUDA 12/13, ROCm, Intel, Metal |

## Text-to-Speech

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [piper](https://github.com/rhasspy/piper) | Fast neural TTS | CPU |
| [Coqui TTS](https://github.com/idiap/coqui-ai-TTS) | TTS with 1100+ languages and voice cloning | CPU, CUDA 12/13, ROCm, Intel, Metal |
| [Kokoro](https://huggingface.co/hexgrad/Kokoro-82M) | Lightweight TTS (82M params) | CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [Chatterbox](https://github.com/resemble-ai/chatterbox) | Production-grade TTS with emotion control | CPU, CUDA 12/13, Metal, Jetson L4T |
| [VibeVoice](https://github.com/microsoft/VibeVoice) | Real-time TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [Qwen3-TTS](https://github.com/QwenLM/Qwen3-TTS) | TTS with custom voice, voice design, and voice cloning | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [fish-speech](https://github.com/fishaudio/fish-speech) | High-quality TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [Pocket TTS](https://github.com/kyutai-labs/pocket-tts) | Lightweight CPU-efficient TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |
| [OuteTTS](https://github.com/OuteAI/outetts) | TTS with custom speaker voices | CPU, CUDA 12 |
| [faster-qwen3-tts](https://github.com/andimarafioti/faster-qwen3-tts) | Real-time Qwen3-TTS with CUDA graph capture | CUDA 12/13, Jetson L4T |
| [NeuTTS Air](https://github.com/neuphonic/neutts-air) | Instant voice cloning TTS | CPU, CUDA 12, ROCm |
| [VoxCPM](https://github.com/ModelBest/VoxCPM) | Expressive end-to-end TTS | CPU, CUDA 12/13, ROCm, Intel, Metal |
| [Kitten TTS](https://github.com/KittenML/KittenTTS) | Kitten TTS model | CPU, Metal |
| [MLX-Audio](https://github.com/Blaizzy/mlx-audio) | Audio models on Apple Silicon | Metal, CPU, CUDA 12/13, Jetson L4T |

## Music Generation

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [ACE-Step](https://github.com/ace-step/ACE-Step-1.5) | Music generation from text descriptions, lyrics, or audio | CPU, CUDA 12/13, ROCm, Intel, Metal |
| [acestep.cpp](https://github.com/ace-step/acestep.cpp) | ACE-Step 1.5 C++ backend using GGML | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |

## Image & Video Generation

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp) | Stable Diffusion, Flux, PhotoMaker in C/C++ | CPU, CUDA 12/13, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [diffusers](https://github.com/huggingface/diffusers) | HuggingFace diffusion models (image and video generation) | CPU, CUDA 12/13, ROCm, Intel, Metal, Jetson L4T |

## Specialized Tasks

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [RF-DETR](https://github.com/roboflow/rf-detr) | Real-time transformer-based object detection | CPU, CUDA 12/13, Intel, Metal, Jetson L4T |
| [rerankers](https://github.com/AnswerDotAI/rerankers) | Document reranking for RAG | CUDA 12/13, ROCm, Intel, Metal |
| [local-store](https://github.com/mudler/LocalAI) | Local vector database for embeddings | CPU, Metal |
| [Silero VAD](https://github.com/snakers4/silero-vad) | Voice Activity Detection | CPU |
| [TRL](https://github.com/huggingface/trl) | Fine-tuning (SFT, DPO, GRPO, RLOO, KTO, ORPO) | CPU, CUDA 12/13 |
| [llama.cpp quantization](https://github.com/ggml-org/llama.cpp) | HuggingFace → GGUF model conversion and quantization | CPU, Metal |
| [Opus](https://opus-codec.org/) | Audio codec for WebRTC / Realtime API | CPU, Metal |

## Acceleration Support Summary

### GPU Acceleration
- **NVIDIA CUDA**: CUDA 12.0, CUDA 13.0 support across most backends
- **AMD ROCm**: HIP-based acceleration for AMD GPUs
- **Intel oneAPI**: SYCL-based acceleration for Intel GPUs (F16/F32 precision)
- **Vulkan**: Cross-platform GPU acceleration
- **Metal**: Apple Silicon GPU acceleration (M1/M2/M3+)

### Specialized Hardware
- **NVIDIA Jetson (L4T CUDA 12)**: ARM64 support for embedded AI (AGX Orin, Jetson Nano, Jetson Xavier NX, Jetson AGX Xavier)
- **NVIDIA Jetson (L4T CUDA 13)**: ARM64 support for embedded AI (DGX Spark)
- **Apple Silicon**: Native Metal acceleration for Mac M1/M2/M3+
- **Darwin x86**: Intel Mac support

### CPU Optimization
- **AVX/AVX2/AVX512**: Advanced vector extensions for x86
- **Quantization**: 4-bit, 5-bit, 8-bit integer quantization support
- **Mixed Precision**: F16/F32 mixed precision support

Note: any backend name listed above can be used in the `backend` field of the model configuration file (See [the advanced section]({{%relref "advanced" %}})).

- \* Only for CUDA and OpenVINO CPU/XPU acceleration.
