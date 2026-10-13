---
title: "Experimental VirtGPU containers on macOS"
description: "Build a LocalAI llama.cpp guest backend for an APIR-enabled Podman machine on Apple Silicon."
weight: 13
---

GGML-VirtGPU forwards inference operations from a Linux guest to a GGML backend on the host, such as Metal.
This guide uses LocalAI's existing llama.cpp build to create a custom `linux/arm64` image.

{{% notice note %}}
This is an experimental build recipe, not a published LocalAI gallery variant.
The recipe needs validation on an Apple Silicon host with the APIR remoting stack.
LocalAI has no VirtGPU performance measurements for this configuration.
{{% /notice %}}

## Choose a GPU path

| Path | Host setup | LocalAI setup |
| --- | --- | --- |
| Native Metal | macOS | [Native installation]({{% relref "getting-started/macos" %}}) with a Metal backend |
| Vulkan/Venus | Podman with libkrun and Venus | `localai/localai:latest-gpu-vulkan`, with `/dev/dri` available in the container |
| GGML-VirtGPU | APIR-enabled virglrenderer, libkrun, and GGML host libraries | Custom guest build described below |

Vulkan/Venus is the existing container option. VirtGPU aims to reduce remoting overhead by forwarding GGML operations to the host backend.
An ordinary Podman or Docker VM does not provide the required APIR stack automatically.

## Prepare the host

Follow the llama.cpp [VirtGPU development guide](https://github.com/ggml-org/llama.cpp/blob/master/docs/backend/VirtGPU/development.md) to build the host libraries and configure virglrenderer.
Use the llama.cpp revision in `LLAMA_VERSION` in LocalAI's `backend/cpp/llama-cpp/Makefile` for both host and guest.
Keep those builds together when updating LocalAI.

Configure these variables in the environment that starts the macOS hypervisor:

| Variable | Value |
| --- | --- |
| `CONTAINERS_MACHINE_PROVIDER` | `libkrun` |
| `DYLD_LIBRARY_PATH` | Directory containing the APIR-enabled virglrenderer library |
| `VIRGL_APIR_BACKEND_LIBRARY` | Absolute host path to `libggml-virtgpu-backend.dylib` |
| `APIR_LLAMA_CPP_GGML_LIBRARY_PATH` | Absolute host path to `libggml-metal.dylib` |
| `APIR_LLAMA_CPP_GGML_LIBRARY_REG` | `ggml_backend_metal_reg` |

These paths refer to files on macOS. Setting them inside the LocalAI container does not configure the hypervisor.
Use a dedicated Podman machine for this experiment.
Select libkrun when creating that machine; changing the environment does not convert an existing machine to another provider.

Choose the capset configuration described in the upstream [configuration guide](https://github.com/ggml-org/llama.cpp/blob/master/docs/backend/VirtGPU/configuration.md):

- For a hypervisor with APIR capset support, set `GGML_REMOTING_USE_APIR_CAPSET=1` in the guest container.
- For the transitional Venus capset route, set `VIRGL_ROUTE_VENUS_TO_APIR=1` on the host and leave `GGML_REMOTING_USE_APIR_CAPSET` unset in the guest.

The transitional route disables normal Vulkan/Venus operation for that hypervisor.
Do not use it for the Vulkan image in the table above.
With libkrun, upstream documents a 64 GB combined guest RAM and GPU memory limit.
Increasing guest RAM reduces the memory available for GPU allocations within that limit.

Before building LocalAI, verify upstream llama.cpp inference through the configured VM.
Check that the host loads the intended virglrenderer and Metal libraries.
See the [upstream VirtGPU overview](https://github.com/ggml-org/llama.cpp/blob/master/docs/backend/VirtGPU.md) for platform limitations.

## Build the guest images

Run these commands from a fresh LocalAI source checkout on your Mac.
The Podman machine must support `linux/arm64` builds and `RUN --mount` instructions.
The backend build compiles gRPC and llama.cpp, so allow time and disk space for a full native build.

First, add the DRM development package to the Ubuntu builder base:

```sh
podman build --platform linux/arm64 \
  -t localhost/localai-virtgpu-base:dev -f - . <<'EOF'
FROM docker.io/library/ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends libdrm-dev \
    && rm -rf /var/lib/apt/lists/*
EOF
```

Build the existing llama.cpp backend with the VirtGPU guest frontend enabled:

```sh
podman build --platform linux/arm64 \
  --build-arg BASE_IMAGE=localhost/localai-virtgpu-base:dev \
  --build-arg BUILD_TYPE= \
  --build-arg CMAKE_ARGS='-DGGML_VIRTGPU=ON -DGGML_VIRTGPU_BACKEND=OFF' \
  -f backend/Dockerfile.llama-cpp \
  -t localhost/localai-virtgpu-backend:dev .
```

`CMAKE_ARGS` is the container build argument.
For a direct Makefile build with the C++/gRPC dependencies already installed, the corresponding command is:

```sh
make -C backend/cpp/llama-cpp grpc-server \
  EXTRA_CMAKE_ARGS='-DGGML_VIRTGPU=ON -DGGML_VIRTGPU_BACKEND=OFF'
```

The container backend image contains an unpackable backend package, not a complete LocalAI server.
Copy it into a server image under a distinct backend name:

```sh
podman build --platform linux/arm64 \
  -t localhost/localai-virtgpu:dev -f - . <<'EOF'
FROM localhost/localai-virtgpu-backend:dev AS backend
FROM docker.io/localai/localai:latest
USER root
RUN apt-get update && apt-get install -y --no-install-recommends libdrm2 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=backend / /var/lib/local-ai/backends/manual-virtgpu/
EOF
```

This uses the [system backend directory]({{% relref "features/backends#system-provided-backends" %}}).
The name `manual-virtgpu` belongs to this local image; it is not a gallery entry or an automatic hardware preference.
For reproducible deployments, record the LocalAI source commit and pin the server image by digest.

## Configure and run LocalAI

Put a llama.cpp-compatible model at `models/model.gguf`.
Create `models/virtgpu.yaml`:

```yaml
name: virtgpu-test
backend: manual-virtgpu
parameters:
  model: model.gguf
context_size: 4096
gpu_layers: 999
```

For the APIR capset configuration, run:

```sh
podman run --rm --name localai-virtgpu \
  --device /dev/dri \
  -p 127.0.0.1:8080:8080 \
  -e GGML_REMOTING_USE_APIR_CAPSET=1 \
  -e LOCALAI_MODELS_PATH=/models \
  -e LOCALAI_BACKENDS_SYSTEM_PATH=/var/lib/local-ai/backends \
  -v "$PWD/models:/models" \
  localhost/localai-virtgpu:dev
```

For the transitional Venus route, omit the `GGML_REMOTING_USE_APIR_CAPSET` line.
Do not set it to `0`: upstream treats its presence as enabled.
The device mount exposes the guest's `/dev/dri`; it does not expose a macOS device directly.

From another terminal, send a request:

```sh
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"virtgpu-test","messages":[{"role":"user","content":"Say hello."}],"max_tokens":32}'
```

## Verify GPU use

A successful text response alone does not prove GPU use. Check the backend logs for VirtGPU device discovery and GPU layer offload.
Check the host APIR logs for the corresponding Metal work.
Compare against a native run only after both checks pass, using the same weights, context size, and generation settings.

If the backend cannot find a device, check `/dev/dri` inside the VM and container, device permissions, and the capset configuration.
If host initialization fails, check the hypervisor's environment and library paths before changing the LocalAI model configuration.
Rebuild the host and guest together after a llama.cpp revision change.
