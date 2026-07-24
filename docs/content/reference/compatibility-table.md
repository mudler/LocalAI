
+++
disableToc = false
title = "Model compatibility table"
weight = 24
url = "/model-compatibility/"
+++

Besides llama based models, LocalAI is compatible also with other architectures. The table below lists all the backends, compatible models families and the associated repository.

{{% notice note %}}

LocalAI will attempt to automatically load models which are not explicitly configured for a specific backend. You can specify the backend to use by configuring a model with a YAML file. See [the advanced section]({{%relref "advanced" %}}) for more details.

All backends listed here can be installed on demand from the [Backend Gallery]({{%relref "features/backends" %}}). The exact set of acceleration variants published for each backend is defined in [`backend/index.yaml`](https://github.com/mudler/LocalAI/blob/master/backend/index.yaml).

 {{% /notice %}}

## Text Generation & Language Models

| Backend | Description | Capability | Embeddings | Streaming | Acceleration |
|---------|-------------|------------|------------|-----------|-------------|
| [llama.cpp](https://github.com/ggerganov/llama.cpp) | LLM inference in C/C++. Supports LLaMA, Mamba, RWKV, Falcon, Starcoder, GPT-2, [and many others](https://github.com/ggerganov/llama.cpp?tab=readme-ov-file#description) | GPT, Functions | yes | yes | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [ik_llama.cpp](https://github.com/ikawrakow/ik_llama.cpp) | Hard fork of llama.cpp optimized for CPU/hybrid CPU+GPU with IQK quants, custom quant mixes, and MLA for DeepSeek | GPT | yes | yes | CPU (AVX2+) |
| [turboquant](https://github.com/TheTom/llama-cpp-turboquant) | llama.cpp fork adding the TurboQuant KV-cache quantization scheme | GPT | yes | yes | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Jetson L4T |
| [ds4](https://github.com/antirez/ds4) | DeepSeek V4 Flash single-model inference engine, optimized for Metal and CUDA | GPT | no | yes | CPU, CUDA 12/13, Metal, Jetson L4T |
| [vLLM](https://github.com/vllm-project/vllm) | Fast LLM serving with PagedAttention; GPTQ/AWQ/FP8 quantization | GPT, Functions, Multimodal | no | yes | CUDA 12/13, ROCm, Intel SYCL, Jetson L4T |
| [vLLM Omni](https://github.com/vllm-project/vllm-omni) | Unified multimodal generation (text, image, video, audio) on top of vLLM | Multimodal GPT, Functions | no | yes | CUDA 12/13, ROCm, Jetson L4T |
| [SGLang](https://github.com/sgl-project/sglang) | Fast serving framework for LLMs and vision-language models with speculative decoding | GPT, Functions, Multimodal | no | yes | CUDA 12/13, ROCm, Intel SYCL, Jetson L4T |
| [transformers](https://github.com/huggingface/transformers) | HuggingFace Transformers framework | GPT, Embeddings, Multimodal | yes | yes* | CUDA 12/13, ROCm, Intel SYCL, Metal |
| [MLX](https://github.com/ml-explore/mlx-lm) | Apple Silicon LLM inference | GPT, Functions | no | yes | CPU, CUDA 12/13, Metal, Jetson L4T |
| [MLX-VLM](https://github.com/Blaizzy/mlx-vlm) | Vision-Language Models on Apple Silicon | Multimodal GPT, Functions | no | yes | CPU, CUDA 12/13, Metal, Jetson L4T |
| [MLX Distributed](https://github.com/ml-explore/mlx-lm) | Distributed LLM inference across multiple Apple Silicon Macs | GPT | no | no | CPU, CUDA 12/13, Metal, Jetson L4T |
| [tinygrad](https://github.com/tinygrad/tinygrad) | Minimalist deep-learning framework with zero runtime dependencies | GPT, Embeddings, Multimodal | yes | yes | CPU |

## Speech-to-Text

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [whisper.cpp](https://github.com/ggml-org/whisper.cpp) | OpenAI Whisper in C/C++ | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [faster-whisper](https://github.com/SYSTRAN/faster-whisper) | Fast Whisper with CTranslate2 | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [WhisperX](https://github.com/m-bain/whisperX) | Word-level timestamps and speaker diarization | CPU, CUDA 12/13, Metal, Jetson L4T |
| [moonshine](https://github.com/moonshine-ai/moonshine) | Ultra-fast transcription for low-end devices (ONNX) | CPU, CUDA 12/13, Metal |
| [parakeet.cpp](https://github.com/mudler/parakeet.cpp) | C++/GGML port of NVIDIA NeMo Parakeet (tdt/ctc/rnnt/hybrid), with cache-aware streaming | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [CrispASR](https://github.com/CrispStrobe/CrispASR) | Unified speech engine (whisper.cpp fork) supporting Parakeet, Canary, and many ASR architectures, plus TTS | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [voxtral](https://github.com/mudler/voxtral.c) | Voxtral Realtime 4B speech-to-text in pure C | CPU, Metal |
| [Qwen3-ASR](https://github.com/QwenLM/Qwen3-ASR) | Qwen3 automatic speech recognition | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [NeMo](https://github.com/NVIDIA/NeMo) | NVIDIA NeMo ASR toolkit | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal |
| [sherpa-onnx](https://k2-fsa.github.io/sherpa/onnx/) | Sherpa-ONNX ASR (Whisper, Paraformer, SenseVoice) and TTS | CPU, CUDA 12, Metal |

## Text-to-Speech

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [piper](https://github.com/rhasspy/piper) | Fast neural TTS | CPU, Metal |
| [Coqui TTS](https://github.com/idiap/coqui-ai-TTS) | TTS with 1100+ languages and voice cloning | CUDA 12, ROCm, Intel SYCL, Metal |
| [Kokoro](https://huggingface.co/hexgrad/Kokoro-82M) | Lightweight TTS (82M params) | CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [Kokoros](https://huggingface.co/hexgrad/Kokoro-82M) | Pure Rust Kokoro TTS via ONNX | CPU |
| [Chatterbox](https://github.com/resemble-ai/chatterbox) | Production-grade TTS with emotion control | CPU, CUDA 12/13, Metal, Jetson L4T |
| [VibeVoice](https://github.com/microsoft/VibeVoice) | Real-time TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [vibevoice.cpp](https://github.com/mudler/vibevoice.cpp) | Native C++/GGML port of VibeVoice for TTS (voice cloning) and long-form ASR with diarization | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [moss-tts.cpp](https://github.com/mudler/moss-tts.cpp) | Native C++/GGML port of OpenMOSS MOSS-TTS-Local v1.5: 48 kHz stereo TTS with reference-audio voice cloning | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [Qwen3-TTS](https://github.com/QwenLM/Qwen3-TTS) | TTS with custom voice, voice design, and voice cloning | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [qwentts.cpp](https://github.com/ServeurpersoCom/qwentts.cpp) | Native C++/GGML Qwen3-TTS with streaming, named speakers, and voice design | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [OmniVoice](https://github.com/ServeurpersoCom/omnivoice.cpp) | Native C++/GGML TTS with voice cloning, voice design, and streaming | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [fish-speech](https://github.com/fishaudio/fish-speech) | High-quality TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [Pocket TTS](https://github.com/kyutai-labs/pocket-tts) | Lightweight CPU-efficient TTS with voice cloning | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [OuteTTS](https://github.com/OuteAI/outetts) | TTS with custom speaker voices | CPU, CUDA 12 |
| [faster-qwen3-tts](https://github.com/andimarafioti/faster-qwen3-tts) | Real-time Qwen3-TTS with CUDA graph capture | CPU, CUDA 12/13, Jetson L4T |
| [NeuTTS Air](https://github.com/neuphonic/neutts-air) | Instant voice cloning, on-device TTS | CPU, CUDA 12, ROCm |
| [VoxCPM](https://github.com/ModelBest/VoxCPM) | Expressive end-to-end TTS | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal |
| [Kitten TTS](https://github.com/KittenML/KittenTTS) | Kitten TTS model | CPU, Metal |
| [Supertonic](https://github.com/supertone-inc/supertonic) | Lightning-fast on-device multilingual TTS via ONNX | CPU |
| [MLX-Audio](https://github.com/Blaizzy/mlx-audio) | Audio models on Apple Silicon | CPU, CUDA 12/13, Metal, Jetson L4T |
| [liquid-audio](https://github.com/Liquid4All/liquid-audio) | LFM2 end-to-end speech-to-speech, ASR, and TTS | CPU, CUDA 12/13, ROCm, Intel SYCL, Jetson L4T |

## Music & Sound Generation

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [ACE-Step](https://github.com/ace-step/ACE-Step-1.5) | Music generation from text descriptions, lyrics, or audio | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal |
| [acestep.cpp](https://github.com/ace-step/acestep.cpp) | ACE-Step 1.5 C++ backend using GGML | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |

## Image & Video Generation

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp) | Stable Diffusion, Flux, PhotoMaker, Ideogram in C/C++ | CPU, CUDA 12/13, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [diffusers](https://github.com/huggingface/diffusers) | HuggingFace diffusion models (image and video generation) | CPU, CUDA 12/13, ROCm, Intel SYCL, Metal, Jetson L4T |
| [vLLM Omni](https://github.com/vllm-project/vllm-omni) | Multimodal generation including text-to-image and text-to-video | CUDA 12/13, ROCm, Jetson L4T |

## Vision, Detection & Recognition

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [RF-DETR](https://github.com/roboflow/rf-detr) | Real-time transformer-based object detection (Python) | CPU, CUDA 12/13, Intel SYCL, Metal, Jetson L4T |
| [rf-detr.cpp](https://github.com/localai-org/rf-detr.cpp) | Native RF-DETR object detection and instance segmentation in C/C++ using GGML | CPU, CUDA 12/13, Intel SYCL, Vulkan, Jetson L4T |
| [locate-anything.cpp](https://github.com/mudler/locate-anything.cpp) | Open-vocabulary object detection and visual grounding (LocateAnything-3B) in C/C++ using GGML | CPU, CUDA 12/13, Intel SYCL, Vulkan, Jetson L4T |
| [depth-anything.cpp](https://github.com/mudler/depth-anything.cpp) | Depth Anything 3 monocular metric depth + camera pose in C/C++ using GGML | CPU, CUDA 12/13, Intel SYCL, Vulkan, Jetson L4T |
| [sam3.cpp](https://github.com/PABannier/sam3.cpp) | Segment Anything (SAM 3/2/EdgeTAM) with text/point/box prompts in C/C++ using GGML | CPU, CUDA 12/13, Intel SYCL, Vulkan, Jetson L4T |
| [face-detect.cpp](https://github.com/mudler/face-detect.cpp) | Native face detection, recognition, embedding, demographics and anti-spoofing (SCRFD/ArcFace, YuNet/SFace) in C/C++ using GGML | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [voice-detect.cpp](https://github.com/localai-org/voice-detect.cpp) | Native speaker (voice) recognition and voice analysis (ECAPA-TDNN, WeSpeaker, ERes2Net, CAM++, wav2vec2) in C/C++ using GGML | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Metal, Jetson L4T |
| [insightface](https://github.com/deepinsight/insightface) | Face verification, embedding, and anti-spoofing liveness (ONNX Runtime) | CPU, CUDA 12 |
| [speaker-recognition](https://speechbrain.github.io/) | Speaker (voice) recognition via SpeechBrain ECAPA-TDNN | CPU, CUDA 12, Metal |

## Audio Processing

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [Silero VAD](https://github.com/snakers4/silero-vad) | Voice Activity Detection | CPU, Metal |
| [LocalVQE](https://github.com/localai-org/LocalVQE) | Joint acoustic echo cancellation, noise suppression, and dereverberation in C/C++ using GGML | CPU, CUDA 12/13, ROCm, Intel SYCL, Vulkan, Jetson L4T |
| [Opus](https://opus-codec.org/) | Audio codec for WebRTC / Realtime API | CPU, Metal |

## Utilities & Other

| Backend | Description | Acceleration |
|---------|-------------|-------------|
| [rerankers](https://github.com/AnswerDotAI/rerankers) | Document reranking for RAG | CUDA 12, ROCm, Intel SYCL, Metal |
| [privacy-filter.cpp](https://github.com/localai-org/privacy-filter.cpp) | Standalone GGML engine for the openai-privacy-filter PII/NER token-classification model family (powers LocalAI's PII redaction tier) | CPU, CUDA 13, Vulkan |
| [local-store](https://github.com/mudler/LocalAI) | Local-first vector database for embeddings | CPU, Metal |
| [valkey-store](https://github.com/mudler/LocalAI) | Durable vector store for embeddings backed by Valkey Search (FLAT or HNSW) | CPU, Metal |
| [TRL](https://github.com/huggingface/trl) | Fine-tuning (SFT, DPO, GRPO, RLOO, KTO, ORPO) | CPU, CUDA 12/13 |
| [llama.cpp quantization](https://github.com/ggml-org/llama.cpp) | HuggingFace → GGUF model conversion and quantization | CPU, Metal |

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
