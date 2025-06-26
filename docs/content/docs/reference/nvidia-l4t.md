
+++
disableToc = false
title = "Running on Nvidia ARM64"
weight = 27
+++

LocalAI can be run on Nvidia ARM64 devices, such as the Jetson Nano, Jetson Xavier NX, and Jetson AGX Xavier. The following instructions will guide you through building the LocalAI container for Nvidia ARM64 devices.

## Prerequisites

- Docker engine installed (https://docs.docker.com/engine/install/ubuntu/)
- Nvidia container toolkit installed (https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-ap)

## Build the container

Build the LocalAI container for Nvidia ARM64 devices using the following command:

```bash
git clone https://github.com/mudler/LocalAI

cd LocalAI

docker build --build-arg SKIP_DRIVERS=true --build-arg BUILD_TYPE=cublas --build-arg BASE_IMAGE=nvcr.io/nvidia/l4t-jetpack:r36.4.0 --build-arg IMAGE_TYPE=core -t quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-core .
```

Otherwise images are available on quay.io and dockerhub:

```bash
docker pull quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-core
```

## Usage

Run the LocalAI container on Nvidia ARM64 devices using the following command, where `/data/models` is the directory containing the models:

```bash
docker run -e DEBUG=true -p 8080:8080 -v /data/models:/models  -ti --restart=always --name local-ai --runtime nvidia --gpus all quay.io/go-skynet/local-ai:master-nvidia-l4t-arm64-core
```

Note: `/data/models` is the directory containing the models. You can replace it with the directory containing your models.
