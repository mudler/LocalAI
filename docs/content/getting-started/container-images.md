+++
disableToc = false
title = "Run with container images"
weight = 6
url = '/basics/container/'
ico = "rocket_launch"
+++

LocalAI provides a variety of images to support different environments. These images are available on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Docker Hub](https://hub.docker.com/r/localai/localai).

For GPU Acceleration support for Nvidia video graphic cards, use the Nvidia/CUDA images, if you don't have a GPU, use the CPU images. If you have AMD or Mac Silicon, see the [build section]({{%relref "installation/build" %}}).

{{% notice tip %}}

**Available Images Types**:

- Images ending with `-core` are smaller images without predownload python dependencies. Use these images if you plan to use `llama.cpp`, `stablediffusion-ncn` or `rwkv` backends - if you are not sure which one to use, do **not** use these images.

 {{% /notice %}}

#### Prerequisites

Before you begin, ensure you have a container engine installed if you are not using the binaries. Suitable options include Docker or Podman. For installation instructions, refer to the following guides:

- [Install Docker Desktop (Mac, Windows, Linux)](https://docs.docker.com/get-docker/)
- [Install Podman (Linux)](https://podman.io/getting-started/installation)
- [Install Docker engine (Servers)](https://docs.docker.com/engine/install/#get-started)

{{% notice tip %}}

**Hardware Requirements:** The hardware requirements for LocalAI vary based on the model size and quantization method used. For performance benchmarks with different backends, such as `llama.cpp`, visit [this link](https://github.com/ggerganov/llama.cpp#memorydisk-requirements). The `rwkv` backend is noted for its lower resource consumption.

 {{% /notice %}}

## Standard container images

Standard container images do not have pre-installed models. Use these if you want to configure models manually.

{{< tabs >}}
{{% tab title="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `localai/localai:master`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest` | `localai/localai:latest`                  |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `localai/localai:{{< version >}}`             |

{{% /tab %}}

{{% tab title="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-12` | `localai/localai:master-gpu-nvidia-cuda-12`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-12` | `localai/localai:latest-gpu-nvidia-cuda-12`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-12` | `localai/localai:{{< version >}}-gpu-nvidia-cuda-12`             |

{{% /tab %}}

{{% tab title="GPU Images CUDA 13" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-nvidia-cuda-13` | `localai/localai:master-gpu-nvidia-cuda-13`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-nvidia-cuda-13` | `localai/localai:latest-gpu-nvidia-cuda-13`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-nvidia-cuda-13` | `localai/localai:{{< version >}}-gpu-nvidia-cuda-13`             |

{{% /tab %}}

{{% tab title="Intel GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-intel` | `localai/localai:master-gpu-intel`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-intel` | `localai/localai:latest-gpu-intel`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-intel` | `localai/localai:{{< version >}}-gpu-intel`             |

{{% /tab %}}

{{% tab title="AMD GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-gpu-hipblas` | `localai/localai:master-gpu-hipblas`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-hipblas` | `localai/localai:latest-gpu-hipblas`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-gpu-hipblas` | `localai/localai:{{< version >}}-gpu-hipblas`             |

{{% /tab %}}

{{% tab title="Vulkan Images" %}}
| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-vulkan` | `localai/localai:master-vulkan`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-gpu-vulkan` | `localai/localai:latest-gpu-vulkan`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-vulkan` | `localai/localai:{{< version >}}-vulkan`             |
{{% /tab %}}

{{% tab title="Nvidia Linux for tegra (CUDA 12)" %}}

These images are compatible with Nvidia ARM64 devices with CUDA 12, such as the Jetson Nano, Jetson Xavier NX, and Jetson AGX Orin. For more information, see the [Nvidia L4T guide]({{%relref "reference/nvidia-l4t" %}}).

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64` | `localai/localai:master-nvidia-l4t-arm64`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64` | `localai/localai:latest-nvidia-l4t-arm64`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-nvidia-l4t-arm64` | `localai/localai:{{< version >}}-nvidia-l4t-arm64`             |

{{% /tab %}}

{{% tab title="Nvidia Linux for tegra (CUDA 13)" %}}

These images are compatible with Nvidia ARM64 devices with CUDA 13, such as the Nvidia DGX Spark. For more information, see the [Nvidia L4T guide]({{%relref "reference/nvidia-l4t" %}}).

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-cuda-13` | `localai/localai:master-nvidia-l4t-arm64-cuda-13`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64-cuda-13` | `localai/localai:latest-nvidia-l4t-arm64-cuda-13`                 |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-nvidia-l4t-arm64-cuda-13` | `localai/localai:{{< version >}}-nvidia-l4t-arm64-cuda-13`             |

{{% /tab %}}

{{< /tabs >}}

## See Also

- [GPU acceleration]({{%relref "features/gpu-acceleration" %}})
