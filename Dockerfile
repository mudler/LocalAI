ARG GO_VERSION=1.20-bullseye

FROM golang:$GO_VERSION as requirements

ARG BUILD_TYPE
ARG CUDA_MAJOR_VERSION=11
ARG CUDA_MINOR_VERSION=7
ARG SPDLOG_VERSION="1.11.0"
ARG PIPER_PHONEMIZE_VERSION='1.0.0'
ARG TARGETARCH
ARG TARGETVARIANT

ENV BUILD_TYPE=${BUILD_TYPE}
ENV EXTERNAL_GRPC_BACKENDS="huggingface-embeddings:/build/extra/grpc/huggingface/huggingface.py"
ARG GO_TAGS="stablediffusion tts"

RUN apt-get update && \
    apt-get install -y ca-certificates cmake curl patch pip

# Extras requirements
COPY extra/requirements.txt /build/extra/requirements.txt
RUN pip install -r /build/extra/requirements.txt && rm -rf /build/extra/requirements.txt

# CuBLAS requirements
RUN if [ "${BUILD_TYPE}" = "cublas" ]; then \
    apt-get install -y software-properties-common && \
    apt-add-repository contrib && \
    curl -O https://developer.download.nvidia.com/compute/cuda/repos/debian11/x86_64/cuda-keyring_1.0-1_all.deb && \
    dpkg -i cuda-keyring_1.0-1_all.deb && \
    rm -f cuda-keyring_1.0-1_all.deb && \
    apt-get update && \
    apt-get install -y cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} \
    ; fi
ENV PATH /usr/local/cuda/bin:${PATH}

WORKDIR /build

# OpenBLAS requirements
RUN apt-get install -y libopenblas-dev

# Stable Diffusion requirements
RUN apt-get install -y libopencv-dev && \
    ln -s /usr/include/opencv4/opencv2 /usr/include/opencv2

# Use the variables in subsequent instructions
RUN echo "Target Architecture: $TARGETARCH"
RUN echo "Target Variant: $TARGETVARIANT"

# piper requirements
# Use pre-compiled Piper phonemization library (includes onnxruntime)
#RUN if echo "${GO_TAGS}" | grep -q "tts"; then \
RUN test -n "$TARGETARCH" \
    || (echo 'warn: missing $TARGETARCH, either set this `ARG` manually, or run using `docker buildkit`')

RUN curl -L "https://github.com/gabime/spdlog/archive/refs/tags/v${SPDLOG_VERSION}.tar.gz" | \
    tar -xzvf - && \
    mkdir -p "spdlog-${SPDLOG_VERSION}/build" && \
    cd "spdlog-${SPDLOG_VERSION}/build" && \
    cmake ..  && \
    make -j8 && \
    cmake --install . --prefix /usr && mkdir -p "lib/Linux-$(uname -m)" && \
    cd /build && \
    mkdir -p "lib/Linux-$(uname -m)/piper_phonemize" && \
    curl -L "https://github.com/rhasspy/piper-phonemize/releases/download/v${PIPER_PHONEMIZE_VERSION}/libpiper_phonemize-${TARGETARCH:-$(go env GOARCH)}${TARGETVARIANT}.tar.gz" | \
    tar -C "lib/Linux-$(uname -m)/piper_phonemize" -xzvf - && ls -liah /build/lib/Linux-$(uname -m)/piper_phonemize/ && \
    cp -rfv /build/lib/Linux-$(uname -m)/piper_phonemize/lib/. /usr/lib/ && \
    ln -s /usr/lib/libpiper_phonemize.so /usr/lib/libpiper_phonemize.so.1 && \
    cp -rfv /build/lib/Linux-$(uname -m)/piper_phonemize/include/. /usr/include/
# \
#    ; fi

###################################
###################################

FROM requirements as builder

ARG GO_TAGS="stablediffusion tts"

ENV GO_TAGS=${GO_TAGS}
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

WORKDIR /build

COPY Makefile .
RUN make get-sources
COPY go.mod .
RUN make prepare
COPY . .
COPY .git .

RUN ESPEAK_DATA=/build/lib/Linux-$(uname -m)/piper_phonemize/lib/espeak-ng-data make build

###################################
###################################

FROM requirements

ARG FFMPEG

ENV REBUILD=false
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz

# Add FFmpeg
RUN if [ "${FFMPEG}" = "true" ]; then \
    apt-get install -y ffmpeg \
    ; fi

WORKDIR /build

# we start fresh & re-copy all assets because `make build` does not clean up nicely after itself
# so when `entrypoint.sh` runs `make build` again (which it does by default), the build would fail
# see https://github.com/go-skynet/LocalAI/pull/658#discussion_r1241971626 and
# https://github.com/go-skynet/LocalAI/pull/434
COPY . .
RUN make prepare-sources
COPY --from=builder /build/local-ai ./

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f $HEALTHCHECK_ENDPOINT || exit 1

EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]
