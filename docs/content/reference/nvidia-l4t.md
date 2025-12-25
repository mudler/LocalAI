
+++
disableToc = false
title = "Running on Nvidia ARM64"
weight = 27
+++

LocalAI can be run on Nvidia ARM64 devices, such as the Jetson Nano, Jetson Xavier NX, Jetson AGX Orin, and Nvidia DGX Spark. The following instructions will guide you through building and using the LocalAI container for Nvidia ARM64 devices.

## Platform Compatibility

- **CUDA 12 L4T images**: Compatible with Nvidia AGX Orin and similar platforms (Jetson Nano, Jetson Xavier NX, Jetson AGX Xavier)
- **CUDA 13 L4T images**: Compatible with Nvidia DGX Spark

## Prerequisites

- Docker engine installed (https://docs.docker.com/engine/install/ubuntu/)
- Nvidia container toolkit installed (https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-ap)

## Pre-built Images

Pre-built images are available on quay.io and dockerhub:

### CUDA 12 (for AGX Orin and similar platforms)

```bash
docker pull quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64
# or
docker pull localai/localai:latest-nvidia-l4t-arm64
```

### CUDA 13 (for DGX Spark)

```bash
docker pull quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64-cuda-13
# or
docker pull localai/localai:latest-nvidia-l4t-arm64-cuda-13
```

## Build the container

If you need to build the container yourself, use the following commands:

### CUDA 12 (for AGX Orin and similar platforms)

```bash
git clone https://github.com/mudler/LocalAI

cd LocalAI

docker build --build-arg SKIP_DRIVERS=true --build-arg BUILD_TYPE=cublas --build-arg BASE_IMAGE=nvcr.io/nvidia/l4t-jetpack:r36.4.0 --build-arg IMAGE_TYPE=core -t quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-core .
```

### CUDA 13 (for DGX Spark)

```bash
git clone https://github.com/mudler/LocalAI

cd LocalAI

docker build --build-arg SKIP_DRIVERS=false --build-arg BUILD_TYPE=cublas --build-arg CUDA_MAJOR_VERSION=13 --build-arg CUDA_MINOR_VERSION=0 --build-arg BASE_IMAGE=ubuntu:24.04 --build-arg IMAGE_TYPE=core -t quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-cuda-13-core .
```

## Usage

Run the LocalAI container on Nvidia ARM64 devices using the following commands, where `/data/models` is the directory containing the models:

### CUDA 12 (for AGX Orin and similar platforms)

```bash
docker run -e DEBUG=true -p 8080:8080 -v /data/models:/models -ti --restart=always --name local-ai --runtime nvidia --gpus all quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64
```

### CUDA 13 (for DGX Spark)

```bash
docker run -e DEBUG=true -p 8080:8080 -v /data/models:/models -ti --restart=always --name local-ai --runtime nvidia --gpus all quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64-cuda-13
```

Note: `/data/models` is the directory containing the models. You can replace it with the directory containing your models.
