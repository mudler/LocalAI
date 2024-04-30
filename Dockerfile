ARG IMAGE_TYPE=extras
ARG BASE_IMAGE=ubuntu:22.04
ARG GRPC_BASE_IMAGE=${BASE_IMAGE}

# The requirements-core target is common to all images.  It should not be placed in requirements-core unless every single build will use it.
FROM ${BASE_IMAGE} AS requirements-core

USER root

ARG GO_VERSION=1.21.7
ARG TARGETARCH
ARG TARGETVARIANT

ENV DEBIAN_FRONTEND=noninteractive
ENV EXTERNAL_GRPC_BACKENDS="coqui:/build/backend/python/coqui/run.sh,huggingface-embeddings:/build/backend/python/sentencetransformers/run.sh,petals:/build/backend/python/petals/run.sh,transformers:/build/backend/python/transformers/run.sh,sentencetransformers:/build/backend/python/sentencetransformers/run.sh,rerankers:/build/backend/python/rerankers/run.sh,autogptq:/build/backend/python/autogptq/run.sh,bark:/build/backend/python/bark/run.sh,diffusers:/build/backend/python/diffusers/run.sh,exllama:/build/backend/python/exllama/run.sh,vall-e-x:/build/backend/python/vall-e-x/run.sh,vllm:/build/backend/python/vllm/run.sh,mamba:/build/backend/python/mamba/run.sh,exllama2:/build/backend/python/exllama2/run.sh,transformers-musicgen:/build/backend/python/transformers-musicgen/run.sh,parler-tts:/build/backend/python/parler-tts/run.sh"

ARG GO_TAGS="stablediffusion tinydream tts"

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        cmake \
        curl \
        git \
        python3-pip \
        python-is-python3 \
        unzip && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* && \
    pip install --upgrade pip

# Install Go
RUN curl -L -s https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH $PATH:/root/go/bin:/usr/local/go/bin

# Install grpc compilers
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Install grpcio-tools (the version in 22.04 is too old)
RUN pip install --user grpcio-tools

COPY --chmod=644 custom-ca-certs/* /usr/local/share/ca-certificates/
RUN update-ca-certificates

# Use the variables in subsequent instructions
RUN echo "Target Architecture: $TARGETARCH"
RUN echo "Target Variant: $TARGETVARIANT"

# Cuda
ENV PATH /usr/local/cuda/bin:${PATH}

# HipBLAS requirements
ENV PATH /opt/rocm/bin:${PATH}

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

RUN test -n "$TARGETARCH" \
    || (echo 'warn: missing $TARGETARCH, either set this `ARG` manually, or run using `docker buildkit`')

###################################
###################################

# The requirements-extras target is for any builds with IMAGE_TYPE=extras. It should not be placed in this target unless every IMAGE_TYPE=extras build will use it
FROM requirements-core AS requirements-extras

RUN apt-get update && \
    apt-get install -y --no-install-recommends gpg && \
    curl https://repo.anaconda.com/pkgs/misc/gpgkeys/anaconda.asc | gpg --dearmor > conda.gpg && \
    install -o root -g root -m 644 conda.gpg /usr/share/keyrings/conda-archive-keyring.gpg && \
    gpg --keyring /usr/share/keyrings/conda-archive-keyring.gpg --no-default-keyring --fingerprint 34161F5BF5EB1D4BFBBB8F0A8AEB4F8B29D82806 && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/conda-archive-keyring.gpg] https://repo.anaconda.com/pkgs/misc/debrepo/conda stable main" > /etc/apt/sources.list.d/conda.list && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/conda-archive-keyring.gpg] https://repo.anaconda.com/pkgs/misc/debrepo/conda stable main" | tee -a /etc/apt/sources.list.d/conda.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        conda && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

ENV PATH="/root/.cargo/bin:${PATH}"

RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        espeak-ng \
        espeak && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

###################################
###################################

# The requirements-drivers target is for BUILD_TYPE specific items.  If you need to install something specific to CUDA, or specific to ROCM, it goes here.
# This target will be built on top of requirements-core or requirements-extras as retermined by the IMAGE_TYPE build-arg
FROM requirements-${IMAGE_TYPE} AS requirements-drivers

ARG BUILD_TYPE
ARG CUDA_MAJOR_VERSION=11
ARG CUDA_MINOR_VERSION=7

ENV BUILD_TYPE=${BUILD_TYPE}

# CuBLAS requirements
RUN if [ "${BUILD_TYPE}" = "cublas" ]; then \
        apt-get update && \
        apt-get install -y  --no-install-recommends \
            software-properties-common && \
        curl -O https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/cuda-keyring_1.1-1_all.deb && \
        dpkg -i cuda-keyring_1.1-1_all.deb && \
        rm -f cuda-keyring_1.1-1_all.deb && \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcurand-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcusparse-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
            libcusolver-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* \
    ; fi

# If we are building with clblas support, we need the libraries for the builds
RUN if [ "${BUILD_TYPE}" = "clblas" ]; then \
        apt-get update && \
        apt-get install -y --no-install-recommends \
            libclblast-dev && \
        apt-get clean && \
        rm -rf /var/lib/apt/lists/* \
    ; fi

###################################
###################################

# The grpc target does one thing, it builds and installs GRPC.  This is in it's own layer so that it can be effectively cached by CI.
# You probably don't need to change anything here, and if you do, make sure that CI is adjusted so that the cache continues to work.
FROM ${GRPC_BASE_IMAGE} AS grpc

# This is a bit of a hack, but it's required in order to be able to effectively cache this layer in CI
ARG GRPC_MAKEFLAGS="-j4 -Otarget"
ARG GRPC_VERSION=v1.58.0

ENV MAKEFLAGS=${GRPC_MAKEFLAGS}

WORKDIR /build

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        build-essential \
        cmake \
        git && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# We install GRPC to a different prefix here so that we can copy in only the build artifacts later
# saves several hundred MB on the final docker image size vs copying in the entire GRPC source tree
# and running make install in the target container
RUN git clone --recurse-submodules --jobs 4 -b ${GRPC_VERSION} --depth 1 --shallow-submodules https://github.com/grpc/grpc && \
    mkdir -p /build/grpc/cmake/build && \
    cd /build/grpc/cmake/build && \
    cmake -DgRPC_INSTALL=ON -DgRPC_BUILD_TESTS=OFF -DCMAKE_INSTALL_PREFIX:PATH=/opt/grpc ../.. && \
    make && \
    make install && \
    rm -rf /build

###################################
###################################

# The builder target compiles LocalAI. This target is not the target that will be uploaded to the registry.
# Adjustments to the build process should likely be made here.
FROM requirements-drivers AS builder

ARG GO_TAGS="stablediffusion tts"
ARG GRPC_BACKENDS
ARG MAKEFLAGS

ENV GRPC_BACKENDS=${GRPC_BACKENDS}
ENV GO_TAGS=${GO_TAGS}
ENV MAKEFLAGS=${MAKEFLAGS}
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

WORKDIR /build

COPY . .
COPY .git .
RUN echo "GO_TAGS: $GO_TAGS"

RUN make prepare

# We need protoc installed, and the version in 22.04 is too old.  We will create one as part installing the GRPC build below
# but that will also being in a newer version of absl which stablediffusion cannot compile with.  This version of protoc is only
# here so that we can generate the grpc code for the stablediffusion build
RUN curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v26.1/protoc-26.1-linux-x86_64.zip -o protoc.zip && \
    unzip -j -d /usr/local/bin protoc.zip bin/protoc && \
    rm protoc.zip

# stablediffusion does not tolerate a newer version of abseil, build it first
RUN GRPC_BACKENDS=backend-assets/grpc/stablediffusion make build

# Install the pre-built GRPC
COPY --from=grpc /opt/grpc /usr/local

# Rebuild with defaults backends
WORKDIR /build
RUN make build

RUN if [ ! -d "/build/sources/go-piper/piper-phonemize/pi/lib/" ]; then \
        mkdir -p /build/sources/go-piper/piper-phonemize/pi/lib/ \
        touch /build/sources/go-piper/piper-phonemize/pi/lib/keep \
    ; fi

###################################
###################################

# This is the final target. The result of this target will be the image uploaded to the registry.
# If you cannot find a more suitable place for an addition, this layer is a suitable place for it.
FROM requirements-drivers

ARG FFMPEG
ARG BUILD_TYPE
ARG TARGETARCH
ARG IMAGE_TYPE=extras
ARG MAKEFLAGS

ENV BUILD_TYPE=${BUILD_TYPE}
ENV REBUILD=false
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz
ENV MAKEFLAGS=${MAKEFLAGS}

ARG CUDA_MAJOR_VERSION=11
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all
ENV PIP_CACHE_PURGE=true

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
COPY --from=builder /build/backend-assets/grpc/stablediffusion ./backend-assets/grpc/stablediffusion

## Duplicated from Makefile to avoid having a big layer that's hard to push
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/autogptq \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/bark \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/diffusers \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/vllm \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/mamba \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/sentencetransformers \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/rerankers \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/transformers \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/vall-e-x \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/exllama \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/exllama2 \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/petals \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/transformers-musicgen \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/parler-tts \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
    make -C backend/python/coqui \
    ; fi

# Make sure the models directory exists
RUN mkdir -p /build/models

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f ${HEALTHCHECK_ENDPOINT} || exit 1
  
VOLUME /build/models
EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]
