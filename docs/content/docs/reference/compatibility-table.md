
+++
disableToc = false
title = "Model compatibility table"
weight = 24
url = "/model-compatibility/"
+++

Besides llama based models, LocalAI is compatible also with other architectures. The table below lists all the backends, compatible models families and the associated repository.

{{% alert note %}}

LocalAI will attempt to automatically load models which are not explicitly configured for a specific backend. You can specify the backend to use by configuring a model with a YAML file. See [the advanced section]({{%relref "docs/advanced" %}}) for more details.

{{% /alert %}}

## Text Generation & Language Models

{{< table "table-responsive" >}}
| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Capability | Embeddings support                | Token stream support | Acceleration |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|--------------|
| [llama.cpp]({{%relref "docs/features/text-generation#llama.cpp" %}})        | LLama, Mamba, RWKV, Falcon, Starcoder, GPT-2, [and many others](https://github.com/ggerganov/llama.cpp?tab=readme-ov-file#description) | yes                      | GPT and Functions                        | yes | yes                  | CUDA 11/12, ROCm, Intel SYCL, Vulkan, Metal, CPU |
| [vLLM](https://github.com/vllm-project/vllm)        | Various GPTs and quantization formats | yes                      | GPT             | no | no                  | CUDA 12, ROCm, Intel |
| [transformers](https://github.com/huggingface/transformers) | Various GPTs and quantization formats  | yes                      | GPT, embeddings, Audio generation            | yes | yes*                  | CUDA 11/12, ROCm, Intel, CPU |
| [exllama2](https://github.com/turboderp-org/exllamav2)  | GPTQ                   | yes                       | GPT only                  | no                               | no                   | CUDA 12 |
| [MLX](https://github.com/ml-explore/mlx-lm)        | Various LLMs               | yes                       | GPT                        | no                                | no                   | Metal (Apple Silicon) |
| [MLX-VLM](https://github.com/Blaizzy/mlx-vlm)        | Vision-Language Models               | yes                       | Multimodal GPT                        | no                                | no                   | Metal (Apple Silicon) |
| [langchain-huggingface](https://github.com/tmc/langchaingo)                                                                    | Any text generators available on HuggingFace through API | yes                      | GPT                        | no                                | no                   | N/A |
{{< /table >}}

## Audio & Speech Processing

{{< table "table-responsive" >}}
| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Capability | Embeddings support                | Token stream support | Acceleration |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|--------------|
| [whisper.cpp](https://github.com/ggml-org/whisper.cpp)         | whisper               | no                       | Audio transcription                 | no                                | no                   | CUDA 12, ROCm, Intel SYCL, Vulkan, CPU |
| [faster-whisper](https://github.com/SYSTRAN/faster-whisper)         | whisper               | no                       | Audio transcription                 | no                                | no                   | CUDA 12, ROCm, Intel, CPU |
| [piper](https://github.com/rhasspy/piper) ([binding](https://github.com/mudler/go-piper))                                                                     | Any piper onnx model | no                      | Text to voice                        | no                                | no                   | CPU |
| [bark](https://github.com/suno-ai/bark)  | bark                   | no                       | Audio generation                  | no                               | no                   | CUDA 12, ROCm, Intel |
| [bark-cpp](https://github.com/PABannier/bark.cpp)        | bark               | no                       | Audio-Only                 | no                                | no                   | CUDA, Metal, CPU |
| [coqui](https://github.com/idiap/coqui-ai-TTS) | Coqui TTS    | no                       | Audio generation and Voice cloning    | no                               | no                   | CUDA 12, ROCm, Intel, CPU |
| [kokoro](https://github.com/hexgrad/kokoro) | Kokoro TTS    | no                       | Text-to-speech    | no                               | no                   | CUDA 12, ROCm, Intel, CPU |
| [chatterbox](https://github.com/resemble-ai/chatterbox) | Chatterbox TTS    | no                       | Text-to-speech    | no                               | no                   | CUDA 11/12, CPU |
| [kitten-tts](https://github.com/KittenML/KittenTTS) | Kitten TTS    | no                       | Text-to-speech    | no                               | no                   | CPU |
| [silero-vad](https://github.com/snakers4/silero-vad) with [Golang bindings](https://github.com/streamer45/silero-vad-go) | Silero VAD    | no                       | Voice Activity Detection    | no                               | no                   | CPU |
| [neutts](https://github.com/neuphonic/neuttsair) | NeuTTSAir    | no                       | Text-to-speech with voice cloning    | no                               | no                   | CUDA 12, ROCm, CPU |
| [mlx-audio](https://github.com/Blaizzy/mlx-audio) | MLX | no                       | Text-tospeech    | no                               | no                   | Metal (Apple Silicon) |
{{< /table >}}

## Image & Video Generation

{{< table "table-responsive" >}}
| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Capability | Embeddings support                | Token stream support | Acceleration |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|--------------|
| [stablediffusion.cpp](https://github.com/leejet/stable-diffusion.cpp)         | stablediffusion-1, stablediffusion-2, stablediffusion-3, flux, PhotoMaker               | no                       | Image                 | no                                | no                   | CUDA 12, Intel SYCL, Vulkan, CPU |
| [diffusers](https://github.com/huggingface/diffusers)  | SD, various diffusion models,...                   | no                       | Image/Video generation    | no                               | no                   | CUDA 11/12, ROCm, Intel, Metal, CPU |
| [transformers-musicgen](https://github.com/huggingface/transformers)  | MusicGen                    | no                       | Audio generation                | no                               | no                   | CUDA, CPU |
{{< /table >}}

## Specialized AI Tasks

{{< table "table-responsive" >}}
| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Capability | Embeddings support                | Token stream support | Acceleration |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|--------------|
| [rfdetr](https://github.com/roboflow/rf-detr) | RF-DETR    | no                       | Object Detection    | no                               | no                   | CUDA 12, Intel, CPU |
| [rerankers](https://github.com/AnswerDotAI/rerankers) | Reranking API    | no                       | Reranking   | no                               | no                   | CUDA 11/12, ROCm, Intel, CPU |
| [local-store](https://github.com/mudler/LocalAI) | Vector database    | no                       | Vector storage   | yes                               | no                   | CPU |
| [huggingface](https://huggingface.co/docs/hub/en/api) | HuggingFace API models    | yes                       | Various AI tasks   | yes                               | yes                   | API-based |
{{< /table >}}

## Acceleration Support Summary

### GPU Acceleration
- **NVIDIA CUDA**: CUDA 11.7, CUDA 12.0 support across most backends
- **AMD ROCm**: HIP-based acceleration for AMD GPUs
- **Intel oneAPI**: SYCL-based acceleration for Intel GPUs (F16/F32 precision)
- **Vulkan**: Cross-platform GPU acceleration
- **Metal**: Apple Silicon GPU acceleration (M1/M2/M3+)

### Specialized Hardware
- **NVIDIA Jetson (L4T)**: ARM64 support for embedded AI
- **Apple Silicon**: Native Metal acceleration for Mac M1/M2/M3+
- **Darwin x86**: Intel Mac support

### CPU Optimization
- **AVX/AVX2/AVX512**: Advanced vector extensions for x86
- **Quantization**: 4-bit, 5-bit, 8-bit integer quantization support
- **Mixed Precision**: F16/F32 mixed precision support

Note: any backend name listed above can be used in the `backend` field of the model configuration file (See [the advanced section]({{%relref "docs/advanced" %}})).

- \* Only for CUDA and OpenVINO CPU/XPU acceleration.
