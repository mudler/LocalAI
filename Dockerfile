ARG IMAGE_TYPE=extras
ARG BASE_IMAGE=ubuntu:22.04
ARG GRPC_BASE_IMAGE=${BASE_IMAGE}
ARG INTEL_BASE_IMAGE=${BASE_IMAGE}

# The requirements-core target is common to all images.  It should not be placed in requirements-core unless every single build will use it.
FROM ${BASE_IMAGE} AS requirements-core

USER root

ARG GO_VERSION=1.22.6
ARG CMAKE_VERSION=3.26.4
ARG CMAKE_FROM_SOURCE=false
ARG TARGETARCH
ARG TARGETVARIANT

ENV DEBIAN_FRONTEND=noninteractive
ENV EXTERNAL_GRPC_BACKENDS="coqui:/build/backend/python/coqui/run.sh,huggingface-embeddings:/build/backend/python/sentencetransformers/run.sh,transformers:/build/backend/python/transformers/run.sh,sentencetransformers:/build/backend/python/sentencetransformers/run.sh,rerankers:/build/backend/python/rerankers/run.sh,autogptq:/build/backend/python/autogptq/run.sh,bark:/build/backend/python/bark/run.sh,diffusers:/build/backend/python/diffusers/run.sh,openvoice:/build/backend/python/openvoice/run.sh,vall-e-x:/build/backend/python/vall-e-x/run.sh,vllm:/build/backend/python/vllm/run.sh,mamba:/build/backend/python/mamba/run.sh,exllama2:/build/backend/python/exllama2/run.sh,transformers-musicgen:/build/backend/python/transformers-musicgen/run.sh,parler-tts:/build/backend/python/parler-tts/run.sh"


RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        ccache \
        ca-certificates \
        curl libssl-dev \
        git \
        unzip upx-ucl && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install CMake (the version in 22.04 is too old)
RUN <<EOT bash
    if [ "${CMAKE_FROM_SOURCE}}" = "true" ]; then
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

RUN test -n "$TARGETARCH" \
    || (echo 'warn: missing $TARGETARCH, either set this `ARG` manually, or run using `docker buildkit`')

# Use the variables in subsequent instructions
RUN echo "Target Architecture: $TARGETARCH"
RUN echo "Target Variant: $TARGETVARIANT"

# Cuda
ENV PATH=/usr/local/cuda/bin:${PATH}

# HipBLAS requirements
ENV PATH=/opt/rocm/bin:${PATH}

# OpenBLAS requirements and stable diffusion
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        libopenblas-dev \
        libopencv-dev && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Set up OpenCV
RUN ln -s /usr/include/opencv4/opencv2 /usr/include/opencv2

WORKDIR /build

###################################
###################################

# The requirements-extras target is for any builds with IMAGE_TYPE=extras. It should not be placed in this target unless every IMAGE_TYPE=extras build will use it
FROM requirements-core AS requirements-extras

# Install uv as a system package
RUN curl -LsSf https://astral.sh/uv/install.sh | UV_INSTALL_DIR=/usr/bin sh
ENV PATH="/root/.cargo/bin:${PATH}"

RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        espeak-ng \
        espeak \
        python3-pip \
        python-is-python3 \
        python3-dev llvm \
        python3-venv && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* && \
    pip install --upgrade pip

# Install grpcio-tools (the version in 22.04 is too old)
RUN pip install --user grpcio-tools

###################################
###################################

# The requirements-drivers target is for BUILD_TYPE specific items.  If you need to install something specific to CUDA, or specific to ROCM, it goes here.
# This target will be built on top of requirements-core or requirements-extras as retermined by the IMAGE_TYPE build-arg
FROM requirements-${IMAGE_TYPE} AS requirements-drivers

ARG BUILD_TYPE
ARG CUDA_MAJOR_VERSION=12
ARG CUDA_MINOR_VERSION=0
ARG SKIP_DRIVERS=false

ENV BUILD_TYPE=${BUILD_TYPE}

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

###################################
###################################

# Temporary workaround for Intel's repository to work correctly
# https://community.intel.com/t5/Intel-oneAPI-Math-Kernel-Library/APT-Repository-not-working-signatures-invalid/m-p/1599436/highlight/true#M36143
# This is a temporary workaround until Intel fixes their repository
FROM ${INTEL_BASE_IMAGE} AS intel
RUN wget -qO - https://repositories.intel.com/gpu/intel-graphics.key | \
gpg --yes --dearmor --output /usr/share/keyrings/intel-graphics.gpg
RUN echo "deb [arch=amd64 signed-by=/usr/share/keyrings/intel-graphics.gpg] https://repositories.intel.com/gpu/ubuntu jammy/lts/2350 unified" > /etc/apt/sources.list.d/intel-graphics.list

###################################
###################################

# The grpc target does one thing, it builds and installs GRPC.  This is in it's own layer so that it can be effectively cached by CI.
# You probably don't need to change anything here, and if you do, make sure that CI is adjusted so that the cache continues to work.
FROM ${GRPC_BASE_IMAGE} AS grpc

# This is a bit of a hack, but it's required in order to be able to effectively cache this layer in CI
ARG GRPC_MAKEFLAGS="-j4 -Otarget"
ARG GRPC_VERSION=v1.65.0
ARG CMAKE_FROM_SOURCE=false
ARG CMAKE_VERSION=3.26.4

ENV MAKEFLAGS=${GRPC_MAKEFLAGS}

WORKDIR /build

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        build-essential curl libssl-dev \
        git && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Install CMake (the version in 22.04 is too old)
RUN <<EOT bash
    if [ "${CMAKE_FROM_SOURCE}}" = "true" ]; then
        curl -L -s https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}.tar.gz -o cmake.tar.gz && tar xvf cmake.tar.gz && cd cmake-${CMAKE_VERSION} && ./configure && make && make install
    else
        apt-get update && \
        apt-get install -y \
            cmake && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/*
    fi
EOT

# We install GRPC to a different prefix here so that we can copy in only the build artifacts later
# saves several hundred MB on the final docker image size vs copying in the entire GRPC source tree
# and running make install in the target container
RUN git clone --recurse-submodules --jobs 4 -b ${GRPC_VERSION} --depth 1 --shallow-submodules https://github.com/grpc/grpc && \
    mkdir -p /build/grpc/cmake/build && \
    cd /build/grpc/cmake/build && \
    sed -i "216i\  TESTONLY" "../../third_party/abseil-cpp/absl/container/CMakeLists.txt" && \
    cmake -DgRPC_INSTALL=ON -DgRPC_BUILD_TESTS=OFF -DCMAKE_INSTALL_PREFIX:PATH=/opt/grpc ../.. && \
    make && \
    make install && \
    rm -rf /build

###################################
###################################

# The builder-base target has the arguments, variables, and copies shared between full builder images and the uncompiled devcontainer

FROM requirements-drivers AS builder-base

ARG GO_TAGS="stablediffusion tts p2p"
ARG GRPC_BACKENDS
ARG MAKEFLAGS
ARG LD_FLAGS="-s -w"

ENV GRPC_BACKENDS=${GRPC_BACKENDS}
ENV GO_TAGS=${GO_TAGS}
ENV MAKEFLAGS=${MAKEFLAGS}
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all
ENV LD_FLAGS=${LD_FLAGS}

RUN echo "GO_TAGS: $GO_TAGS" && echo "TARGETARCH: $TARGETARCH"

WORKDIR /build


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


###################################
###################################

# This first portion of builder holds the layers specifically used to build backend-assets/grpc/stablediffusion
# In most cases, builder is the image you should be using - however, this can save build time if one just needs to copy backend-assets/grpc/stablediffusion and nothing else.
FROM builder-base AS builder-sd

# stablediffusion does not tolerate a newer version of abseil, copy only over enough elements to build it
COPY Makefile .
COPY go.mod .
COPY go.sum .
COPY backend/backend.proto ./backend/backend.proto
COPY backend/go/image/stablediffusion ./backend/go/image/stablediffusion
COPY pkg/grpc ./pkg/grpc
COPY pkg/stablediffusion ./pkg/stablediffusion
RUN git init
RUN make sources/go-stable-diffusion
RUN touch prepare-sources

# Actually build the backend
RUN GRPC_BACKENDS=backend-assets/grpc/stablediffusion make backend-assets/grpc/stablediffusion

###################################
###################################

# The builder target compiles LocalAI. This target is not the target that will be uploaded to the registry.
# Adjustments to the build process should likely be made here.
FROM builder-sd AS builder

# Install the pre-built GRPC
COPY --from=grpc /opt/grpc /usr/local

# Rebuild with defaults backends
WORKDIR /build

COPY . .
COPY .git .

RUN make prepare

## Build the binary
## If it's CUDA or hipblas, we want to skip some of the llama-compat backends to save space
## We only leave the most CPU-optimized variant and the fallback for the cublas/hipblas build
## (both will use CUDA or hipblas for the actual computation)
RUN if [ "${BUILD_TYPE}" = "cublas" ] || [ "${BUILD_TYPE}" = "hipblas" ]; then \
        SKIP_GRPC_BACKEND="backend-assets/grpc/llama-cpp-avx backend-assets/grpc/llama-cpp-avx2" make build; \
    else \
        make build; \
    fi

RUN if [ ! -d "/build/sources/go-piper/piper-phonemize/pi/lib/" ]; then \
        mkdir -p /build/sources/go-piper/piper-phonemize/pi/lib/ \
        touch /build/sources/go-piper/piper-phonemize/pi/lib/keep \
    ; fi

###################################
###################################

# The devcontainer target is not used on CI. It is a target for developers to use locally -
# rather than copying files it mounts them locally and leaves building to the developer

FROM builder-base AS devcontainer

ARG FFMPEG

COPY --from=grpc /opt/grpc /usr/local

COPY --from=builder-sd /build/backend-assets/grpc/stablediffusion /build/backend-assets/grpc/stablediffusion

COPY .devcontainer-scripts /.devcontainer-scripts

# Add FFmpeg
RUN if [ "${FFMPEG}" = "true" ]; then \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            ffmpeg && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* \
    ; fi

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ssh less wget
# For the devcontainer, leave apt functional in case additional devtools are needed at runtime.

RUN go install github.com/go-delve/delve/cmd/dlv@latest

RUN go install github.com/mikefarah/yq/v4@latest

###################################
###################################

# This is the final target. The result of this target will be the image uploaded to the registry.
# If you cannot find a more suitable place for an addition, this layer is a suitable place for it.
FROM requirements-drivers

ARG FFMPEG
ARG BUILD_TYPE
ARG TARGETARCH
ARG IMAGE_TYPE=extras
ARG EXTRA_BACKENDS
ARG MAKEFLAGS

ENV BUILD_TYPE=${BUILD_TYPE}
ENV REBUILD=false
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz
ENV MAKEFLAGS=${MAKEFLAGS}

ARG CUDA_MAJOR_VERSION=12
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

# Add FFmpeg
RUN if [ "${FFMPEG}" = "true" ]; then \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            ffmpeg && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* \
    ; fi

WORKDIR /build

# we start fresh & re-copy all assets because `make build` does not clean up nicely after itself
# so when `entrypoint.sh` runs `make build` again (which it does by default), the build would fail
# see https://github.com/go-skynet/LocalAI/pull/658#discussion_r1241971626 and
# https://github.com/go-skynet/LocalAI/pull/434
COPY . .

COPY --from=builder /build/sources ./sources/
COPY --from=grpc /opt/grpc /usr/local

RUN make prepare-sources

# Copy the binary
COPY --from=builder /build/local-ai ./

# Copy shared libraries for piper
COPY --from=builder /build/sources/go-piper/piper-phonemize/pi/lib/* /usr/lib/

# do not let stablediffusion rebuild (requires an older version of absl)
COPY --from=builder-sd /build/backend-assets/grpc/stablediffusion ./backend-assets/grpc/stablediffusion

# Change the shell to bash so we can use [[ tests below
SHELL ["/bin/bash", "-c"]
# We try to strike a balance between individual layer size (as that affects total push time) and total image size
# Splitting the backends into more groups with fewer items results in a larger image, but a smaller size for the largest layer
# Splitting the backends into fewer groups with more items results in a smaller image, but a larger size for the largest layer

RUN if [[ ( "${EXTRA_BACKENDS}" =~ "coqui" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/coqui \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "parler-tts" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/parler-tts \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "diffusers" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/diffusers \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "transformers-musicgen" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/transformers-musicgen \
    ; fi

RUN if [[ ( "${EXTRA_BACKENDS}" =~ "vall-e-x" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/vall-e-x \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "openvoice" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/openvoice \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "sentencetransformers" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/sentencetransformers \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "exllama2" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/exllama2 \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "transformers" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/transformers \
    ; fi

RUN if [[ ( "${EXTRA_BACKENDS}" =~ "vllm" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/vllm \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "autogptq" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/autogptq \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "bark" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/bark \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "rerankers" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/rerankers \
    ; fi && \
    if [[ ( "${EXTRA_BACKENDS}" =~ "mamba" || -z "${EXTRA_BACKENDS}" ) && "$IMAGE_TYPE" == "extras" ]]; then \
        make -C backend/python/mamba \
    ; fi

# Make sure the models directory exists
RUN mkdir -p /build/models

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f ${HEALTHCHECK_ENDPOINT} || exit 1

VOLUME /build/models
EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]
