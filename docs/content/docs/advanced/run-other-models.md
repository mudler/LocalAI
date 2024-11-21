+++
disableToc = false
title = "Run other Models"
weight = 23
icon = "rocket_launch"

+++

## Running other models

> _Do you have already a model file? Skip to [Run models manually]({{%relref "docs/getting-started/models" %}})_.

To load models into LocalAI, you can either [use models manually]({{%relref "docs/getting-started/models" %}}) or configure LocalAI to pull the models from external sources, like Huggingface and configure the model.

To do that, you can point LocalAI to an URL to a YAML configuration file - however - LocalAI does also have some popular model configuration embedded in the binary as well. Below you can find a list of the models configuration that LocalAI has pre-built, see [Model customization]({{%relref "docs/getting-started/customize-model" %}}) on how to configure models from URLs.

There are different categories of models: [LLMs]({{%relref "docs/features/text-generation" %}}), [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) , [Embeddings]({{%relref "docs/features/embeddings" %}}), [Audio to Text]({{%relref "docs/features/audio-to-text" %}}), and [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) depending on the backend being used and the model architecture.

{{% alert icon="💡" %}}

To customize the models, see [Model customization]({{%relref "docs/getting-started/customize-model" %}}). For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI-examples/tree/main/configurations) and the configurations for the models below is available [here](https://github.com/mudler/LocalAI/tree/master/embedded/models).
{{% /alert %}}

{{< tabs tabTotal="3" >}}
{{% tab tabName="CPU-only" %}}

> 💡Don't need GPU acceleration? use the CPU images which are lighter and do not have Nvidia dependencies

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core phi-2``` |
| 🌋 [bakllava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core bakllava``` |
| 🌋 [llava-1.5](https://llava-vl.github.io/) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava-1.5``` |
| 🌋 [llava-1.6-mistral](https://huggingface.co/cjpais/llava-1.6-mistral-7b-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava-1.6-mistral``` |
| 🌋 [llava-1.6-vicuna](https://huggingface.co/cmp-nct/llava-1.6-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava-1.6-vicuna``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X)  | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) | GPU-only |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) (with transformers) | [LLM]({{%relref "docs/features/text-generation" %}}) | GPU-only |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) (with llama.cpp) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core codellama-7b-gguf``` |
| [hermes-2-pro-mistral](https://huggingface.co/NousResearch/Hermes-2-Pro-Mistral-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core hermes-2-pro-mistral``` |
{{% /tab %}}

{{% tab tabName="GPU (CUDA 11)" %}}


> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core phi-2``` |
| 🌋 [bakllava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core bakllava``` |
| 🌋 [llava-1.5](https://llava-vl.github.io/) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda11-core llava-1.5``` |
| 🌋 [llava-1.6-mistral](https://huggingface.co/cjpais/llava-1.6-mistral-7b-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda11-core llava-1.6-mistral``` |
| 🌋 [llava-1.6-vicuna](https://huggingface.co/cmp-nct/llava-1.6-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda11-core llava-1.6-vicuna``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 mamba-chat``` |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) |  ```docker run -ti -p 8080:8080 -e COMPEL=0 --gpus all localai/localai:{{< version >}}-cublas-cuda11 animagine-xl``` |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 transformers-tinyllama``` |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11 codellama-7b``` |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core codellama-7b-gguf``` |
| [hermes-2-pro-mistral](https://huggingface.co/NousResearch/Hermes-2-Pro-Mistral-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda11-core hermes-2-pro-mistral``` |
{{% /tab %}}


{{% tab tabName="GPU (CUDA 12)" %}}

> To know which version of CUDA do you have available, you can check with `nvidia-smi` or `nvcc --version` see also [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}}).

| Model | Category | Docker command |
| --- | --- | --- |
| [phi-2](https://huggingface.co/microsoft/phi-2) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core phi-2``` |
| 🌋 [bakllava](https://github.com/SkunkworksAI/BakLLaVA) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core bakllava``` |
| 🌋 [llava-1.5](https://llava-vl.github.io/) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda12-core llava-1.5``` |
| 🌋 [llava-1.6-mistral](https://huggingface.co/cjpais/llava-1.6-mistral-7b-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda12-core llava-1.6-mistral``` |
| 🌋 [llava-1.6-vicuna](https://huggingface.co/cmp-nct/llava-1.6-gguf) | [Multimodal LLM]({{%relref "docs/features/gpt-vision" %}}) | ```docker run -ti -p 8080:8080 localai/localai:{{< version >}}-cublas-cuda12-core llava-1.6-vicuna``` |
| [mistral-openorca](https://huggingface.co/Open-Orca/Mistral-7B-OpenOrca) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mistral-openorca``` |
| [bert-cpp](https://github.com/skeskinen/bert.cpp) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core bert-cpp``` |
| [all-minilm-l6-v2](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2) | [Embeddings]({{%relref "docs/features/embeddings" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 all-minilm-l6-v2``` |
| whisper-base | [Audio to Text]({{%relref "docs/features/audio-to-text" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core whisper-base``` |
| rhasspy-voice-en-us-amy | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core rhasspy-voice-en-us-amy``` |
| 🐸 [coqui](https://github.com/coqui-ai/TTS) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 coqui``` |
| 🐶 [bark](https://github.com/suno-ai/bark) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 bark``` |
| 🔊 [vall-e-x](https://github.com/Plachtaa/VALL-E-X) | [Text to Audio]({{%relref "docs/features/text-to-audio" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 vall-e-x``` |
| mixtral-instruct Mixtral-8x7B-Instruct-v0.1 | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core mixtral-instruct``` |
| [tinyllama-chat](https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF) [original model](https://huggingface.co/TinyLlama/TinyLlama-1.1B-Chat-v0.3) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core tinyllama-chat``` |
| [dolphin-2.5-mixtral-8x7b](https://huggingface.co/TheBloke/dolphin-2.5-mixtral-8x7b-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core dolphin-2.5-mixtral-8x7b``` |
| 🐍 [mamba](https://github.com/state-spaces/mamba) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 mamba-chat``` |
| animagine-xl | [Text to Image]({{%relref "docs/features/image-generation" %}}) | ```docker run -ti -p 8080:8080 -e COMPEL=0 --gpus all localai/localai:{{< version >}}-cublas-cuda12 animagine-xl``` |
| transformers-tinyllama | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 transformers-tinyllama``` |
| [codellama-7b](https://huggingface.co/codellama/CodeLlama-7b-hf) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12 codellama-7b``` |
| [codellama-7b-gguf](https://huggingface.co/TheBloke/CodeLlama-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}})  | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core codellama-7b-gguf``` |
| [hermes-2-pro-mistral](https://huggingface.co/NousResearch/Hermes-2-Pro-Mistral-7B-GGUF) | [LLM]({{%relref "docs/features/text-generation" %}}) | ```docker run -ti -p 8080:8080 --gpus all localai/localai:{{< version >}}-cublas-cuda12-core hermes-2-pro-mistral``` |
{{% /tab %}}

{{< /tabs >}}

{{% alert icon="💡" %}}
**Tip** You can actually specify multiple models to start an instance with the models loaded, for example to have both llava and phi-2 configured:

```bash
docker run -ti -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core llava phi-2
```

{{% /alert %}}
