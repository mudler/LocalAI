+++
disableToc = false
title = "Run with container images"
weight = 6
url = '/basics/container/'
ico = "rocket_launch"
+++

LocalAI provides a variety of images to support different environments. These images are available on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Docker Hub](https://hub.docker.com/r/localai/localai).

All-in-One images comes with a pre-configured set of models and backends, standard images instead do not have any model pre-configured and installed.

For GPU Acceleration support for Nvidia video graphic cards, use the Nvidia/CUDA images, if you don't have a GPU, use the CPU images. If you have AMD or Mac Silicon, see the [build section]({{%relref "docs/getting-started/build" %}}).

{{% alert icon="💡" %}}

**Available Images Types**:

- Images ending with `-core` are smaller images without predownload python dependencies. Use these images if you plan to use `llama.cpp`, `stablediffusion-ncn` or `rwkv` backends - if you are not sure which one to use, do **not** use these images.
- Images containing the `aio` tag are all-in-one images with all the features enabled, and come with an opinionated set of configuration.

{{% /alert %}}

#### Prerequisites

Before you begin, ensure you have a container engine installed if you are not using the binaries. Suitable options include Docker or Podman. For installation instructions, refer to the following guides:

- [Install Docker Desktop (Mac, Windows, Linux)](https://docs.docker.com/get-docker/)
- [Install Podman (Linux)](https://podman.io/getting-started/installation)
- [Install Docker engine (Servers)](https://docs.docker.com/engine/install/#get-started)

{{% alert icon="💡" %}}

**Hardware Requirements:** The hardware requirements for LocalAI vary based on the model size and quantization method used. For performance benchmarks with different backends, such as `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements). The `rwkv` backend is noted for its lower resource consumption.

{{% /alert %}}

## All-in-one images

All-In-One images are images that come pre-configured with a set of models and backends to fully leverage almost all the LocalAI featureset. These images are available for both CPU and GPU environments. The AIO images are designed to be easy to use and require no configuration. Models configuration can be found [here](https://github.com/mudler/LocalAI/tree/master/aio) separated by size.

In the AIO images there are models configured with the names of OpenAI models, however, they are really backed by Open Source models. You can find the table below

{{< table "table-responsive" >}}
| Category | Model name | Real model (CPU) | Real model (GPU) |
| ---- | ---- | ---- | ---- |
| Text Generation | `gpt-4` | `phi-2` | `hermes-2-pro-mistral` |
| Multimodal Vision | `gpt-4-vision-preview` | `bakllava` | `llava-1.6-mistral` |
| Image Generation | `stablediffusion` | `stablediffusion` | `dreamshaper-8` |
| Speech to Text | `whisper-1` | `whisper` with `whisper-base` model | <= same |
| Text to Speech | `tts-1` | `en-us-amy-low.onnx` from `rhasspy/piper` | <= same |
| Embeddings | `text-embedding-ada-002` | `all-MiniLM-L6-v2` in Q4 | `all-MiniLM-L6-v2` |
{{< /table >}}

### Usage

Select the image (CPU or GPU) and start the container with Docker:

```bash
# CPU example
docker run -p 8080:8080 --name local-ai -ti localai/localai:latest-aio-cpu
# For Nvidia GPUs:
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-11
# docker run -p 8080:8080 --gpus all --name local-ai -ti localai/localai:latest-aio-gpu-nvidia-cuda-12
```

LocalAI will automatically download all the required models, and the API will be available at [localhost:8080](http://localhost:8080/v1/models).


Or with a docker-compose file:

```yaml
version: "3.9"
services:
  api:
    image: localai/localai:latest-aio-cpu
    # For a specific version:
    # image: localai/localai:{{< version >}}-aio-cpu
    # For Nvidia GPUs decomment one of the following (cuda11 or cuda12):
    # image: localai/localai:{{< version >}}-aio-gpu-nvidia-cuda-11
    # image: localai/localai:{{< version >}}-aio-gpu-nvidia-cuda-12
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-11
    # image: localai/localai:latest-aio-gpu-nvidia-cuda-12
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/readyz"]
      interval: 1m
      timeout: 20m
      retries: 5
    ports:
      - 8080:8080
    environment:
      - DEBUG=true
      # ...
    volumes:
      - ./models:/models:cached
    # decomment the following piece if running with Nvidia GPUs
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - driver: nvidia
    #           count: 1
    #           capabilities: [gpu]
```

{{% alert icon="💡" %}}

**Models caching**: The **AIO** image will download the needed models on the first run if not already present and store those in `/models` inside the container. The AIO models will be automatically updated with new versions of AIO images.

You can change the directory inside the container by specifying a `MODELS_PATH` environment variable (or `--models-path`). 

If you want to use a named model or a local directory, you can mount it as a volume to `/models`:

```bash
docker run -p 8080:8080 --name local-ai -ti -v $PWD/models:/models localai/localai:latest-aio-cpu
```

or associate a volume:

```bash
docker volume create localai-models
docker run -p 8080:8080 --name local-ai -ti -v localai-models:/models localai/localai:latest-aio-cpu
```

{{% /alert %}}

### Available AIO images

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images for CPU | `quay.io/go-skynet/local-ai:latest-aio-cpu` | `localai/localai:latest-aio-cpu`                      |
| Versioned image (e.g. for CPU) | `quay.io/go-skynet/local-ai:{{< version >}}-aio-cpu` | `localai/localai:{{< version >}}-aio-cpu`             |
| Latest images for Nvidia GPU (CUDA11) | `quay.io/go-skynet/local-ai:latest-aio-gpu-nvidia-cuda-11` | `localai/localai:latest-aio-gpu-nvidia-cuda-11`                      |
| Latest images for Nvidia GPU (CUDA12) | `quay.io/go-skynet/local-ai:latest-aio-gpu-nvidia-cuda-12` | `localai/localai:latest-aio-gpu-nvidia-cuda-12`                      |
| Latest images for AMD GPU | `quay.io/go-skynet/local-ai:latest-aio-gpu-hipblas` | `localai/localai:latest-aio-gpu-hipblas`                      |
| Latest images for Intel GPU | `quay.io/go-skynet/local-ai:latest-aio-gpu-intel` | `localai/localai:latest-aio-gpu-intel`                      |

### Available environment variables

The AIO Images are inheriting the same environment variables as the base images and the environment of LocalAI (that you can inspect by calling `--help`). However, it supports additional environment variables available only from the container image

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `PROFILE` | Auto-detected | The size of the model to use. Available: `cpu`, `gpu-8g` |
| `MODELS` | Auto-detected | A list of models YAML Configuration file URI/URL (see also [running models]({{%relref "docs/getting-started/models" %}})) |


## Standard container images

Standard container images do not have pre-installed models. 

{{< tabs tabTotal="8" >}}
{{% tab tabName="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `localai/localai:master`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest` | `localai/localai:latest`                  |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `localai/localai:{{< version >}}`             |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 11" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-11` | `localai/localai:master-gpu-nvidia-cuda-11`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-11` | `localai/localai:latest-gpu-nvidia-cuda-11`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-11` | `localai/localai:{{< version >}}-gpu-nvidia-cuda-11`             |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-12` | `localai/localai:master-gpu-nvidia-cuda-12`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-12` | `localai/localai:latest-gpu-nvidia-cuda-12`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-12` | `localai/localai:{{< version >}}-gpu-nvidia-cuda-12`             |

{{% /tab %}}

{{% tab tabName="Intel GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-intel` | `localai/localai:master-gpu-intel`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-intel` | `localai/localai:latest-gpu-intel`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-intel` | `localai/localai:{{< version >}}-gpu-intel`             |

{{% /tab %}}

{{% tab tabName="AMD GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-hipblas` | `localai/localai:master-gpu-hipblas`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-hipblas` | `localai/localai:latest-gpu-hipblas`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-hipblas` | `localai/localai:{{< version >}}-gpu-hipblas`             |

{{% /tab %}}

{{% tab tabName="Vulkan Images" %}}
| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-vulkan` | `localai/localai:master-vulkan`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-vulkan` | `localai/localai:latest-gpu-vulkan`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-vulkan` | `localai/localai:{{< version >}}-vulkan`             |
{{% /tab %}}

{{% tab tabName="Nvidia Linux for tegra" %}}

These images are compatible with Nvidia ARM64 devices, such as the Jetson Nano, Jetson Xavier NX, and Jetson AGX Xavier. For more information, see the [Nvidia L4T guide]({{%relref "docs/reference/nvidia-l4t" %}}).

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64` | `localai/localai:master-nvidia-l4t-arm64`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64` | `localai/localai:latest-nvidia-l4t-arm64`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-nvidia-l4t-arm64` | `localai/localai:{{< version >}}-nvidia-l4t-arm64`             |

{{% /tab %}}

{{< /tabs >}}

## See Also

- [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}})
