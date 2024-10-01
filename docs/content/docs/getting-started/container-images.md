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

- Images ending with `-core` are smaller images without predownload python dependencies. Use these images if you plan to use `llama.cpp`, `stablediffusion-ncn`, `tinydream` or `rwkv` backends - if you are not sure which one to use, do **not** use these images.
- Images containing the `aio` tag are all-in-one images with all the features enabled, and come with an opinionated set of configuration.
- FFMpeg is **not** included in the default images due to [its licensing](https://www.ffmpeg.org/legal.html). If you need FFMpeg, use the images ending with `-ffmpeg`. Note that `ffmpeg` is needed in case of using `audio-to-text` LocalAI's features.
- If using old and outdated CPUs and no GPUs you might need to set `REBUILD` to `true` as environment variable along with options to disable the flags which your CPU does not support, however note that inference will perform poorly and slow. See also [flagset compatibility]({{%relref "docs/getting-started/build#cpu-flagset-compatibility" %}}).

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
      - ./models:/build/models:cached
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

**Models caching**: The **AIO** image will download the needed models on the first run if not already present and store those in `/build/models` inside the container. The AIO models will be automatically updated with new versions of AIO images.

You can change the directory inside the container by specifying a `MODELS_PATH` environment variable (or `--models-path`). 

If you want to use a named model or a local directory, you can mount it as a volume to `/build/models`:

```bash
docker run -p 8080:8080 --name local-ai -ti -v $PWD/models:/build/models localai/localai:latest-aio-cpu
```

or associate a volume:

```bash
docker volume create localai-models
docker run -p 8080:8080 --name local-ai -ti -v localai-models:/build/models localai/localai:latest-aio-cpu
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
| Latest images for Intel GPU (sycl f16) | `quay.io/go-skynet/local-ai:latest-aio-gpu-intel-f16` | `localai/localai:latest-aio-gpu-intel-f16`                      |
| Latest images for Intel GPU (sycl f32) | `quay.io/go-skynet/local-ai:latest-aio-gpu-intel-f32` | `localai/localai:latest-aio-gpu-intel-f32`                      |

### Available environment variables

The AIO Images are inheriting the same environment variables as the base images and the environment of LocalAI (that you can inspect by calling `--help`). However, it supports additional environment variables available only from the container image

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `PROFILE` | Auto-detected | The size of the model to use. Available: `cpu`, `gpu-8g` |
| `MODELS` | Auto-detected | A list of models YAML Configuration file URI/URL (see also [running models]({{%relref "docs/advanced/run-other-models" %}})) |


## Standard container images

Standard container images do not have pre-installed models. 

Images are available with and without python dependencies. Note that images with python dependencies are bigger (in order of 17GB). 

Images with `core` in the tag are smaller and do not contain any python dependencies. 

{{< tabs tabTotal="7" >}}
{{% tab tabName="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `localai/localai:master`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-cpu`                  | `localai/localai:latest-cpu`                  |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `localai/localai:{{< version >}}`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg` | `localai/localai:{{< version >}}-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg-core` | `localai/localai:{{< version >}}-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 11" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda11` | `localai/localai:master-cublas-cuda11`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-11`                 | `localai/localai:latest-gpu-nvidia-cuda-11`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11` | `localai/localai:{{< version >}}-cublas-cuda11`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda12` | `localai/localai:master-cublas-cuda12`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-12` | `localai/localai:latest-gpu-nvidia-cuda-12`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12` | `localai/localai:{{< version >}}-cublas-cuda12`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="Intel GPU (sycl f16)" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-sycl-f16` | `localai/localai:master-sycl-f16`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-intel-f16` | `localai/localai:latest-gpu-intel-f16`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16` | `localai/localai:{{< version >}}-sycl-f16`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16-ffmpeg` | `localai/localai:{{< version >}}-sycl-f16-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16-ffmpeg-core` | `localai/localai:{{< version >}}-sycl-f16-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="Intel GPU (sycl f32)" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-sycl-f32` | `localai/localai:master-sycl-f32`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-intel-f32` | `localai/localai:latest-gpu-intel-f32`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32` | `localai/localai:{{< version >}}-sycl-f32`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32-ffmpeg` | `localai/localai:{{< version >}}-sycl-f32-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32-ffmpeg-core` | `localai/localai:{{< version >}}-sycl-f32-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="AMD GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-hipblas` | `localai/localai:master-hipblas`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-hipblas`                  | `localai/localai:latest-gpu-hipblas`                  |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-hipblas` | `localai/localai:{{< version >}}-hipblas`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-hipblas-ffmpeg` | `localai/localai:{{< version >}}-hipblas-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-hipblas-ffmpeg-core` | `localai/localai:{{< version >}}-hipblas-ffmpeg-core` |

{{% /tab %}}


{{% tab tabName="Vulkan Images" %}}
| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai: master-vulkan-ffmpeg-core ` | `localai/localai: master-vulkan-ffmpeg-core `                      |
| Latest tag | `quay.io/go-skynet/local-ai: latest-vulkan-ffmpeg-core ` | `localai/localai: latest-vulkan-ffmpeg-core`                 |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-vulkan-fmpeg-core` | `localai/localai:{{< version >}}-vulkan-fmpeg-core`             |
{{% /tab %}}

{{< /tabs >}}

## See Also

- [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}})
