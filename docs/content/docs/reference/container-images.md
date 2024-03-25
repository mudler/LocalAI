
+++
disableToc = false
title = "Available Container images"
weight = 25
+++

LocalAI provides a variety of images to support different environments. These images are available on [quay.io](https://quay.io/repository/go-skynet/local-ai?tab=tags) and [Docker Hub](https://hub.docker.com/r/localai/localai).

> _For All-in-One image with a pre-configured set of models and backends, see the [AIO Images]({{%relref "docs/reference/aio-images" %}})._

For GPU Acceleration support for Nvidia video graphic cards, use the Nvidia/CUDA images, if you don't have a GPU, use the CPU images. If you have AMD or Mac Silicon, see the [build section]({{%relref "docs/getting-started/build" %}}).

{{% alert icon="💡" %}}

**Available Images Types**:

- Images ending with `-core` are smaller images without predownload python dependencies. Use these images if you plan to use `llama.cpp`, `stablediffusion-ncn`, `tinydream` or `rwkv` backends - if you are not sure which one to use, do **not** use these images.
- Images containing the `aio` tag are all-in-one images with all the features enabled, and come with an opinionated set of configuration.
- FFMpeg is **not** included in the default images due to [its licensing](https://www.ffmpeg.org/legal.html). If you need FFMpeg, use the images ending with `-ffmpeg`. Note that `ffmpeg` is needed in case of using `audio-to-text` LocalAI's features.
- If using old and outdated CPUs and no GPUs you might need to set `REBUILD` to `true` as environment variable along with options to disable the flags which your CPU does not support, however note that inference will perform poorly and slow. See also [flagset compatibility]({{%relref "docs/getting-started/build#cpu-flagset-compatibility" %}}).

{{% /alert %}}

{{< tabs tabTotal="6" >}}
{{% tab tabName="Vanilla / CPU Images" %}}

| Description | Quay | Docker Hub                                   |
| --- | --- |-----------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master` | `localai/localai:master`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest` | `localai/localai:latest`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}` | `localai/localai:{{< version >}}`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg` | `localai/localai:{{< version >}}-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg-core` | `localai/localai:{{< version >}}-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 11" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda11` | `localai/localai:master-cublas-cuda11`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-cublas-cuda11` | `localai/localai:latest-cublas-cuda11`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11` | `localai/localai:{{< version >}}-cublas-cuda11`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda11-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="GPU Images CUDA 12" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-cublas-cuda12` | `localai/localai:master-cublas-cuda12`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-cublas-cuda12` | `localai/localai:latest-cublas-cuda12`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12` | `localai/localai:{{< version >}}-cublas-cuda12`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg-core` | `localai/localai:{{< version >}}-cublas-cuda12-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="Intel GPU (sycl f16)" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-sycl-f16` | `localai/localai:master-sycl-f16`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-sycl-f16` | `localai/localai:latest-sycl-f16`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16` | `localai/localai:{{< version >}}-sycl-f16`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16-ffmpeg` | `localai/localai:{{< version >}}-sycl-f16-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16-ffmpeg-core` | `localai/localai:{{< version >}}-sycl-f16-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="Intel GPU (sycl f32)" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-sycl-f32` | `localai/localai:master-sycl-f32`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-sycl-f32` | `localai/localai:latest-sycl-f32`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32` | `localai/localai:{{< version >}}-sycl-f32`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32-ffmpeg` | `localai/localai:{{< version >}}-sycl-f32-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-sycl-f32-ffmpeg-core` | `localai/localai:{{< version >}}-sycl-f32-ffmpeg-core` |

{{% /tab %}}

{{% tab tabName="AMD GPU" %}}

| Description | Quay | Docker Hub                                                  |
| --- | --- |-------------------------------------------------------------|
| Latest images from the branch (development) | `quay.io/go-skynet/local-ai:master-hipblas` | `localai/localai:master-hipblas`                      |
| Latest tag | `quay.io/go-skynet/local-ai:latest-hipblas` | `localai/localai:latest-hipblas`                      |
| Versioned image | `quay.io/go-skynet/local-ai:{{< version >}}-hipblas` | `localai/localai:{{< version >}}-hipblas`             |
| Versioned image including FFMpeg| `quay.io/go-skynet/local-ai:{{< version >}}-hipblas-ffmpeg` | `localai/localai:{{< version >}}-hipblas-ffmpeg`      |
| Versioned image including FFMpeg, no python | `quay.io/go-skynet/local-ai:{{< version >}}-hipblas-ffmpeg-core` | `localai/localai:{{< version >}}-hipblas-ffmpeg-core` |

{{% /tab %}}

{{< /tabs >}}

## See Also

- [GPU acceleration]({{%relref "docs/features/gpu-acceleration" %}})
- [AIO Images]({{%relref "docs/reference/aio-images" %}})