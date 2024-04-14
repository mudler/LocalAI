
+++
disableToc = false
title = "All-In-One images"
weight = 26
+++

All-In-One images are images that come pre-configured with a set of models and backends to fully leverage almost all the LocalAI featureset. These images are available for both CPU and GPU environments. The AIO images are designed to be easy to use and requires no configuration. Models configuration can be found [here](https://github.com/mudler/LocalAI/tree/master/aio) separated by size.

In the AIO images there are models configured with the names of OpenAI models, however, they are really backed by Open Source models. You can find the table below

| Category | Model name | Real model (CPU) | Real model (GPU) |
| ---- | ---- | ---- | ---- |
| Text Generation | `gpt-4` | `phi-2` | `hermes-2-pro-mistral` |
| Multimodal Vision | `gpt-4-vision-preview` | `bakllava` | `llava-1.6-mistral` |
| Image Generation | `stablediffusion` | `stablediffusion` | `dreamshaper-8` |
| Speech to Text | `whisper-1` | `whisper` with `whisper-base` model | <= same |
| Text to Speech | `tts-1` | `en-us-amy-low.onnx` from `rhasspy/piper` | <= same |
| Embeddings | `text-embedding-ada-002` | `all-MiniLM-L6-v2` in Q4 | `all-MiniLM-L6-v2` |

## Usage

Select the image (CPU or GPU) and start the container with Docker:

```bash
# CPU example
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu
```

LocalAI will automatically download all the required models, and the API will be available at [localhost:8080](http://localhost:8080/v1/models).

## Available images

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images for CPU | `quay.io/go-skynet/local-ai:latest-aio-cpu` | `localai/localai:latest-aio-cpu`                      |
| Versioned image (e.g. for CPU) | `quay.io/go-skynet/local-ai:{{< version >}}-aio-cpu` | `localai/localai:{{< version >}}-aio-cpu`             |
| Latest images for Nvidia GPU (CUDA11) | `quay.io/go-skynet/local-ai:latest-aio-gpu-nvidia-cuda-11` | `localai/localai:latest-aio-gpu-nvidia-cuda-11`                      |
| Latest images for Nvidia GPU (CUDA12) | `quay.io/go-skynet/local-ai:latest-aio-gpu-nvidia-cuda-12` | `localai/localai:latest-aio-gpu-nvidia-cuda-12`                      |
| Latest images for AMD GPU | `quay.io/go-skynet/local-ai:latest-aio-gpu-hipblas` | `localai/localai:latest-aio-gpu-hipblas`                      |
| Latest images for Intel GPU (sycl f16) | `quay.io/go-skynet/local-ai:latest-aio-gpu-intel-f16` | `localai/localai:latest-aio-gpu-intel-f16`                      |
| Latest images for Intel GPU (sycl f32) | `quay.io/go-skynet/local-ai:latest-aio-gpu-intel-f32` | `localai/localai:latest-aio-gpu-intel-f32`                      |

## Available environment variables

The AIO Images are inheriting the same environment variables as the base images and the environment of LocalAI (that you can inspect by calling `--help`). However, it supports additional environment variables available only from the container image

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `PROFILE` | Auto-detected | The size of the model to use. Available: `cpu`, `gpu-8g` |
| `MODELS` | Auto-detected | A list of models YAML Configuration file URI/URL (see also [running models]({{%relref "docs/getting-started/run-other-models" %}})) |


