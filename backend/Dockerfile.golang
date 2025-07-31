ARG BASE_IMAGE=ubuntu:22.04

FROM ${BASE_IMAGE} AS builder
ARG BACKEND=rerankers
ARG BUILD_TYPE
ENV BUILD_TYPE=${BUILD_TYPE}
ARG CUDA_MAJOR_VERSION
ARG CUDA_MINOR_VERSION
ARG SKIP_DRIVERS=false
ENV CUDA_MAJOR_VERSION=${CUDA_MAJOR_VERSION}
ENV CUDA_MINOR_VERSION=${CUDA_MINOR_VERSION}
ENV DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH
ARG TARGETVARIANT
ARG GO_VERSION=1.22.6

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        git ccache \
        ca-certificates \
        make cmake \
        curl unzip \
        libssl-dev && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*


# Cuda
ENV PATH=/usr/local/cuda/bin:${PATH}

# HipBLAS requirements
ENV PATH=/opt/rocm/bin:${PATH}

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
        rm -rf /var/lib/apt/lists/*
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
        rm -rf /var/lib/apt/lists/*
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
        # I have no idea why, but the ROCM lib packages don't trigger ldconfig after they install, which results in local-ai and others not being able
        # to locate the libraries. We run ldconfig ourselves to work around this packaging deficiency
        ldconfig \
    ; fi

# Install Go
RUN curl -L -s https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH=$PATH:/root/go/bin:/usr/local/go/bin:/usr/local/bin

# Install grpc compilers
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af
RUN echo "TARGETARCH: $TARGETARCH"

# We need protoc installed, and the version in 22.04 is too old.  We will create one as part installing the GRPC build below
# but that will also being in a newer version of absl which stablediffusion cannot compile with.  This version of protoc is only
# here so that we can generate the grpc code for the stablediffusion build
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

COPY . /LocalAI

RUN cd /LocalAI && make protogen-go && make -C /LocalAI/backend/go/${BACKEND} build

FROM scratch
ARG BACKEND=rerankers

COPY --from=builder /LocalAI/backend/go/${BACKEND}/package/. ./
