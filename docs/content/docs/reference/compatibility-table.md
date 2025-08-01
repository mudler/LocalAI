
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

{{< table "table-responsive" >}}
| Backend and Bindings                                                             | Compatible models     | Completion/Chat endpoint | Capability | Embeddings support                | Token stream support | Acceleration |
|----------------------------------------------------------------------------------|-----------------------|--------------------------|---------------------------|-----------------------------------|----------------------|--------------|
| [llama.cpp]({{%relref "docs/features/text-generation#llama.cpp" %}})        | LLama, Mamba, RWKV, Falcon, Starcoder, GPT-2, [and many others](https://github.com/ggerganov/llama.cpp?tab=readme-ov-file#description) | yes                      | GPT and Functions                        | yes | yes                  | CUDA, openCL, cuBLAS, Metal |
| [whisper](https://github.com/ggerganov/whisper.cpp)         | whisper               | no                       | Audio                 | no                                | no                   | N/A |
| [langchain-huggingface](https://github.com/tmc/langchaingo)                                                                    | Any text generators available on HuggingFace through API | yes                      | GPT                        | no                                | no                   | N/A |
| [piper](https://github.com/rhasspy/piper) ([binding](https://github.com/mudler/go-piper))                                                                     | Any piper onnx model | no                      | Text to voice                        | no                                | no                   | N/A |
| [sentencetransformers](https://github.com/UKPLab/sentence-transformers) | BERT                   | no                       | Embeddings only                  | yes                               | no                   | N/A |
| `bark`  | bark                   | no                       | Audio generation                  | no                               | no                   | yes |
| `autogptq` | GPTQ                   | yes                       | GPT                  | yes                               | no                   | N/A |
| `diffusers`  | SD,...                   | no                       | Image generation    | no                               | no                   | N/A |
| `vllm` | Various GPTs and quantization formats | yes                      | GPT             | no | no                  | CPU/CUDA |
| `exllama2`  | GPTQ                   | yes                       | GPT only                  | no                               | no                   | N/A |
| `transformers-musicgen`  |                    | no                       | Audio generation                | no                               | no                   | N/A |
| stablediffusion               | no                       | Image                 | no                                | no                   | N/A |
| `coqui` | Coqui    | no                       | Audio generation and Voice cloning    | no                               | no                   | CPU/CUDA |
| [rerankers](https://github.com/AnswerDotAI/rerankers) | Reranking API    | no                       | Reranking   | no                               | no                   | CPU/CUDA |
| `transformers` | Various GPTs and quantization formats  | yes                      | GPT, embeddings, Audio generation            | yes | yes*                  | CPU/CUDA/XPU |
| [bark-cpp](https://github.com/PABannier/bark.cpp)        | bark               | no                       | Audio-Only                 | no                                | no                   | yes |
| [stablediffusion-cpp](https://github.com/leejet/stable-diffusion.cpp)         | stablediffusion-1, stablediffusion-2, stablediffusion-3, flux, PhotoMaker               | no                       | Image                 | no                                | no                   | N/A |
| [silero-vad](https://github.com/snakers4/silero-vad) with [Golang bindings](https://github.com/streamer45/silero-vad-go) | Silero VAD    | no                       | Voice Activity Detection    | no                               | no                   | CPU |
{{< /table >}}

Note: any backend name listed above can be used in the `backend` field of the model configuration file (See [the advanced section]({{%relref "docs/advanced" %}})).

- \* Only for CUDA and OpenVINO CPU/XPU acceleration.
