ARG BASE_IMAGE=ubuntu:22.04
ARG GRPC_BASE_IMAGE=${BASE_IMAGE}
ARG INTEL_BASE_IMAGE=${BASE_IMAGE}

FROM ${BASE_IMAGE} AS requirements

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl wget espeak-ng libgomp1 \
        python3 python-is-python3 ffmpeg && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# The requirements-drivers target is for BUILD_TYPE specific items.  If you need to install something specific to CUDA, or specific to ROCM, it goes here.
FROM requirements AS requirements-drivers

ARG BUILD_TYPE
ARG CUDA_MAJOR_VERSION=12
ARG CUDA_MINOR_VERSION=0
ARG SKIP_DRIVERS=false
ARG TARGETARCH
ARG TARGETVARIANT
ENV BUILD_TYPE=${BUILD_TYPE}

RUN mkdir -p /run/localai
RUN echo "default" > /run/localai/capability

# Vulkan requirements
RUN <<EOT bash
    if [ "${BUILD_TYPE}" = "vulkan" ] && [ "${SKIP_DRIVERS}" = "false" ]; then
        apt-get update && \
        apt-get install -y  --no-install-recommends \
            software-properties-common pciutils wget gpg-agent && \
        wget -qO - https://packages.lunarg.com/lunarg-signing-key-pub.asc | apt-key add - && \
        wget -qO /etc/apt/sources.list.d/lunarg-vulkan-jammy.list https://packages.lunarg.com/vulkan/lunarg-vulkan-jammy.list && \
        apt-get update && \
        apt-get install -y \
            vulkan-sdk && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* && \
        echo "vulkan" > /run/localai/capability
    fi
EOT

# CuBLAS requirements
RUN <<EOT bash
    if [ "${BUILD_TYPE}" = "cublas" ] && [ "${SKIP_DRIVERS}" = "false" ]; then
        apt-get update && \
        apt-get install -y  --no-install-recommends \
            software-properties-common pciutils
        if [ "amd64" = "$TARGETARCH" ]; then
            curl -O https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/cuda-keyring_1.1-1_all.deb
        fi
        if [ "arm64" = "$TARGETARCH" ]; then
            curl -O https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/arm64/cuda-keyring_1.1-1_all.deb
        fi
        dpkg -i cuda-keyring_1.1-1_all.deb && \
        rm -f cuda-keyring_1.1-1_all.deb && \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcufft-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcurand-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcusparse-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcusolver-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* && \
        echo "nvidia" > /run/localai/capability
    fi
EOT

RUN <<EOT bash
    if [ "${BUILD_TYPE}" = "cublas" ] && [ "${TARGETARCH}" = "arm64" ]; then
        echo "nvidia-l4t" > /run/localai/capability
    fi
EOT

# If we are building with clblas support, we need the libraries for the builds
RUN if [ "${BUILD_TYPE}" = "clblas" ] && [ "${SKIP_DRIVERS}" = "false" ]; then \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            libclblast-dev && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* \
    ; fi

RUN if [ "${BUILD_TYPE}" = "hipblas" ] && [ "${SKIP_DRIVERS}" = "false" ]; then \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            hipblas-dev \
            rocblas-dev && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* && \
        echo "amd" > /run/localai/capability && \
        # I have no idea why, but the ROCM lib packages don't trigger ldconfig after they install, which results in local-ai and others not being able
        # to locate the libraries. We run ldconfig ourselves to work around this packaging deficiency
        ldconfig \
    ; fi

RUN expr "${BUILD_TYPE}" = intel && echo "intel" > /run/localai/capability || echo "not intel"

# Cuda
ENV PATH=/usr/local/cuda/bin:${PATH}

# HipBLAS requirements
ENV PATH=/opt/rocm/bin:${PATH}

###################################
###################################

# The requirements-core target is common to all images.  It should not be placed in requirements-core unless every single build will use it.
FROM requirements-drivers AS build-requirements

ARG GO_VERSION=1.22.6
ARG CMAKE_VERSION=3.26.4
ARG CMAKE_FROM_SOURCE=false
ARG TARGETARCH
ARG TARGETVARIANT


RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        ccache \
        ca-certificates espeak-ng \
        curl libssl-dev \
        git \
        git-lfs \
        unzip upx-ucl python3 python-is-python3 && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install CMake (the version in 22.04 is too old)
RUN <<EOT bash
    if [ "${CMAKE_FROM_SOURCE}" = "true" ]; then
        curl -L -s https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}.tar.gz -o cmake.tar.gz && tar xvf cmake.tar.gz && cd cmake-${CMAKE_VERSION} && ./configure && make && make install
    else
        apt-get update && \
        apt-get install -y \
            cmake && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/*
    fi
EOT

# Install Go
RUN curl -L -s https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH=$PATH:/root/go/bin:/usr/local/go/bin

# Install grpc compilers
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af

COPY --chmod=644 custom-ca-certs/* /usr/local/share/ca-certificates/
RUN update-ca-certificates


# OpenBLAS requirements and stable diffusion
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        libopenblas-dev && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

RUN test -n "$TARGETARCH" \
    || (echo 'warn: missing $TARGETARCH, either set this `ARG` manually, or run using `docker buildkit`')

# Use the variables in subsequent instructions
RUN echo "Target Architecture: $TARGETARCH"
RUN echo "Target Variant: $TARGETVARIANT"




WORKDIR /build


###################################
###################################

# Temporary workaround for Intel's repository to work correctly
# https://community.intel.com/t5/Intel-oneAPI-Math-Kernel-Library/APT-Repository-not-working-signatures-invalid/m-p/1599436/highlight/true#M36143
# This is a temporary workaround until Intel fixes their repository
FROM ${INTEL_BASE_IMAGE} AS intel
RUN wget -qO - https://repositories.intel.com/gpu/intel-graphics.key | \
gpg --yes --dearmor --output /usr/share/keyrings/intel-graphics.gpg
RUN echo "deb [arch=amd64 signed-by=/usr/share/keyrings/intel-graphics.gpg] https://repositories.intel.com/gpu/ubuntu jammy/lts/2350 unified" > /etc/apt/sources.list.d/intel-graphics.list
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        intel-oneapi-runtime-libs && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

###################################
###################################

# The builder-base target has the arguments, variables, and copies shared between full builder images and the uncompiled devcontainer

FROM build-requirements AS builder-base

ARG GO_TAGS=""
ARG GRPC_BACKENDS
ARG MAKEFLAGS
ARG LD_FLAGS="-s -w"
ARG TARGETARCH
ARG TARGETVARIANT
ENV GRPC_BACKENDS=${GRPC_BACKENDS}
ENV GO_TAGS=${GO_TAGS}
ENV MAKEFLAGS=${MAKEFLAGS}
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all
ENV LD_FLAGS=${LD_FLAGS}

RUN echo "GO_TAGS: $GO_TAGS" && echo "TARGETARCH: $TARGETARCH"

WORKDIR /build


# We need protoc installed, and the version in 22.04 is too old.
RUN <<EOT bash
    if [ "amd64" = "$TARGETARCH" ]; then
        curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-x86_64.zip -o protoc.zip && \
        unzip -j -d /usr/local/bin protoc.zip bin/protoc && \
        rm protoc.zip
    fi
    if [ "arm64" = "$TARGETARCH" ]; then
        curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-aarch_64.zip -o protoc.zip && \
        unzip -j -d /usr/local/bin protoc.zip bin/protoc && \
        rm protoc.zip
    fi
EOT

###################################
###################################

# Compile backends first in a separate stage
FROM builder-base AS builder-backends
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /build

COPY ./Makefile .
COPY ./backend ./backend
COPY ./go.mod .
COPY ./go.sum .
COPY ./.git ./.git

# Some of the Go backends use libs from the main src, we could further optimize the caching by building the CPP backends before here
COPY ./pkg/grpc ./pkg/grpc
COPY ./pkg/utils ./pkg/utils
COPY ./pkg/langchain ./pkg/langchain

RUN ls -l ./
RUN make protogen-go

# The builder target compiles LocalAI. This target is not the target that will be uploaded to the registry.
# Adjustments to the build process should likely be made here.
FROM builder-backends AS builder

WORKDIR /build

COPY . .

## Build the binary
## If we're on arm64 AND using cublas/hipblas, skip some of the llama-compat backends to save space
## Otherwise just run the normal build
RUN make build

###################################
###################################

# The devcontainer target is not used on CI. It is a target for developers to use locally -
# rather than copying files it mounts them locally and leaves building to the developer

FROM builder-base AS devcontainer

COPY .devcontainer-scripts /.devcontainer-scripts

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ssh less
# For the devcontainer, leave apt functional in case additional devtools are needed at runtime.

RUN go install github.com/go-delve/delve/cmd/dlv@latest

RUN go install github.com/mikefarah/yq/v4@latest

###################################
###################################

# This is the final target. The result of this target will be the image uploaded to the registry.
# If you cannot find a more suitable place for an addition, this layer is a suitable place for it.
FROM requirements-drivers

ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz

ARG CUDA_MAJOR_VERSION=12
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

WORKDIR /

COPY ./entrypoint.sh .

# Copy the binary
COPY --from=builder /build/local-ai ./

# Make sure the models directory exists
RUN mkdir -p /models /backends

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f ${HEALTHCHECK_ENDPOINT} || exit 1

VOLUME /models /backends
EXPOSE 8080
ENTRYPOINT [ "/entrypoint.sh" ]
