
+++
disableToc = false
title = "AIKit"
description="AI + BuildKit = AIKit: Build and deploy large language models easily"
weight = 2
+++

GitHub Link - https://github.com/sozercan/aikit

[AIKit](https://github.com/sozercan/aikit) is a quick, easy, and local or cloud-agnostic way to get started to host and deploy large language models (LLMs) for inference. No GPU, internet access or additional tools are needed to get started except for [Docker](https://docs.docker.com/desktop/install/linux-install/)!

AIKit uses [LocalAI](https://localai.io/) under-the-hood to run inference. LocalAI provides a drop-in replacement REST API that is OpenAI API compatible, so you can use any OpenAI API compatible client, such as [Kubectl AI](https://github.com/sozercan/kubectl-ai), [Chatbot-UI](https://github.com/sozercan/chatbot-ui) and many more, to send requests to open-source LLMs powered by AIKit!

> At this time, AIKit is tested with LocalAI `llama` backend. Other backends may work but are not tested. Please open an issue if you'd like to see support for other backends.

## Features

- 🐳 No GPU, Internet access or additional tools needed except for [Docker](https://docs.docker.com/desktop/install/linux-install/)!
- 🤏 Minimal image size, resulting in less vulnerabilities and smaller attack surface with a custom [distroless](https://github.com/GoogleContainerTools/distroless)-based image
- 🚀 Easy to use declarative configuration
- ✨ OpenAI API compatible to use with any OpenAI API compatible client
- 🚢 Kubernetes deployment ready
- 📦 Supports multiple models with a single image
- 🖥️ Supports GPU-accelerated inferencing with NVIDIA GPUs
- 🔐 Signed images for `aikit` and pre-made models

## Pre-made Models

AIKit comes with pre-made models that you can use out-of-the-box!

### CPU
- 🦙 Llama 2 7B Chat: `ghcr.io/sozercan/llama2:7b`
- 🦙 Llama 2 13B Chat: `ghcr.io/sozercan/llama2:13b`
- 🐬 Orca 2 13B: `ghcr.io/sozercan/orca2:13b`

### NVIDIA CUDA

- 🦙 Llama 2 7B Chat (CUDA): `ghcr.io/sozercan/llama2:7b-cuda`
- 🦙 Llama 2 13B Chat (CUDA): `ghcr.io/sozercan/llama2:13b-cuda`
- 🐬 Orca 2 13B (CUDA): `ghcr.io/sozercan/orca2:13b-cuda`

> CUDA models includes CUDA v12. They are used with [NVIDIA GPU acceleration](#gpu-acceleration-support).

## Quick Start

### Creating an image

> This section shows how to create a custom image with models of your choosing. If you want to use one of the pre-made models, skip to [running models](#running-models).
>
> Please see [models folder](./models/) for pre-made model definitions. You can find more model examples at [go-skynet/model-gallery](https://github.com/go-skynet/model-gallery).

Create an `aikitfile.yaml` with the following structure:

```yaml
#syntax=ghcr.io/sozercan/aikit:latest
apiVersion: v1alpha1
models:
  - name: llama-2-7b-chat
    source: https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_K_M.gguf
```

> This is the simplest way to get started to build an image. For full `aikitfile` specification, see [specs](docs/specs.md).

First, create a buildx buildkit instance. Alternatively, if you are using Docker v24 with [containerd image store](https://docs.docker.com/storage/containerd/) enabled, you can skip this step.

```bash
docker buildx create --use --name aikit-builder
```

Then build your image with:

```bash
docker buildx build . -t my-model -f aikitfile.yaml --load
```

This will build a local container image with your model(s). You can see the image with:

```bash
docker images
REPOSITORY    TAG       IMAGE ID       CREATED             SIZE
my-model      latest    e7b7c5a4a2cb   About an hour ago   5.51GB
```

### Running models

You can start the inferencing server for your models with:

```bash
# for pre-made models, replace "my-model" with the image name
docker run -d --rm -p 8080:8080 my-model
```

You can then send requests to `localhost:8080` to run inference from your models. For example:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llama-2-7b-chat",
     "messages": [{"role": "user", "content": "explain kubernetes in a sentence"}]
   }'
{"created":1701236489,"object":"chat.completion","id":"dd1ff40b-31a7-4418-9e32-42151ab6875a","model":"llama-2-7b-chat","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"\nKubernetes is a container orchestration system that automates the deployment, scaling, and management of containerized applications in a microservices architecture."}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}
```

## Kubernetes Deployment

It is easy to get started to deploy your models to Kubernetes!

Make sure you have a Kubernetes cluster running and `kubectl` is configured to talk to it, and your model images are accessible from the cluster.

> You can use [kind](https://kind.sigs.k8s.io/) to create a local Kubernetes cluster for testing purposes.

```bash
# create a deployment
# for pre-made models, replace "my-model" with the image name
kubectl create deployment my-llm-deployment --image=my-model

# expose it as a service
kubectl expose deployment my-llm-deployment --port=8080 --target-port=8080 --name=my-llm-service

# easy to scale up and down as needed
kubectl scale deployment my-llm-deployment --replicas=3

# port-forward for testing locally
kubectl port-forward service/my-llm-service 8080:8080

# send requests to your model
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llama-2-7b-chat",
     "messages": [{"role": "user", "content": "explain kubernetes in a sentence"}]
   }'
{"created":1701236489,"object":"chat.completion","id":"dd1ff40b-31a7-4418-9e32-42151ab6875a","model":"llama-2-7b-chat","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"\nKubernetes is a container orchestration system that automates the deployment, scaling, and management of containerized applications in a microservices architecture."}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}
```

> For an example Kubernetes deployment and service YAML, see [kubernetes folder](./kubernetes/). Please note that these are examples, you may need to customize them (such as properly configured resource requests and limits) based on your needs.

## GPU Acceleration Support

> At this time, only NVIDIA GPU acceleration is supported. Please open an issue if you'd like to see support for other GPU vendors.

### NVIDIA

AIKit supports GPU accelerated inferencing with [NVIDIA Container Toolkit](https://github.com/NVIDIA/nvidia-container-toolkit). You must also have [NVIDIA Drivers](https://www.nvidia.com/en-us/drivers/unix/) installed on your host machine.

For Kubernetes, [NVIDIA GPU Operator](https://github.com/NVIDIA/gpu-operator) provides a streamlined way to install the NVIDIA drivers and container toolkit to configure your cluster to use GPUs.

To get started with GPU-accelerated inferencing, make sure to set the following in your `aikitfile` and build your model.

```yaml
runtime: cuda         # use NVIDIA CUDA runtime
f16: true             # use float16 precision
gpu_layers: 35        # number of layers to offload to GPU
low_vram: true        # for devices with low VRAM
```

> Make sure to customize these values based on your model and GPU specs.

After building the model, you can run it with [`--gpus all`](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/docker-specialized.html#gpu-enumeration) flag to enable GPU support:

```bash
# for pre-made models, replace "my-model" with the image name
docker run --rm --gpus all -p 8080:8080 my-model
```

If GPU acceleration is working, you'll see output that is similar to following in the debug logs:

```bash
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr ggml_init_cublas: found 1 CUDA devices:
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr   Device 0: Tesla T4, compute capability 7.5
...
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: using CUDA for GPU acceleration
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: mem required  =   70.41 MB (+ 2048.00 MB per state)
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: offloading 32 repeating layers to GPU
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: offloading non-repeating layers to GPU
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: offloading v cache to GPU
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: offloading k cache to GPU
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: offloaded 35/35 layers to GPU
5:32AM DBG GRPC(llama-2-7b-chat.Q4_K_M.gguf-127.0.0.1:43735): stderr llm_load_tensors: VRAM used: 5869 MB
```
