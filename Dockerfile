ARG GO_VERSION=1.21-bullseye
ARG IMAGE_TYPE=extras
# extras or core


FROM golang:$GO_VERSION as requirements-core

ARG BUILD_TYPE
ARG CUDA_MAJOR_VERSION=11
ARG CUDA_MINOR_VERSION=7
ARG TARGETARCH
ARG TARGETVARIANT

ENV BUILD_TYPE=${BUILD_TYPE}
ENV EXTERNAL_GRPC_BACKENDS="huggingface-embeddings:/build/backend/python/huggingface/run.sh,autogptq:/build/backend/python/autogptq/run.sh,bark:/build/backend/python/bark/run.sh,diffusers:/build/backend/python/diffusers/run.sh,exllama:/build/backend/python/exllama/run.sh,vall-e-x:/build/backend/python/vall-e-x/run.sh,vllm:/build/backend/python/vllm/run.sh"
ENV GALLERIES='[{"name":"model-gallery", "url":"github:go-skynet/model-gallery/index.yaml"}, {"url": "github:go-skynet/model-gallery/huggingface.yaml","name":"huggingface"}]'
ARG GO_TAGS="stablediffusion tts"

RUN apt-get update && \
    apt-get install -y ca-certificates curl patch pip cmake && apt-get clean


COPY --chmod=644 custom-ca-certs/* /usr/local/share/ca-certificates/
RUN update-ca-certificates

# Use the variables in subsequent instructions
RUN echo "Target Architecture: $TARGETARCH"
RUN echo "Target Variant: $TARGETVARIANT"

# CuBLAS requirements
RUN if [ "${BUILD_TYPE}" = "cublas" ]; then \
    apt-get install -y software-properties-common && \
    apt-add-repository contrib && \
    curl -O https://developer.download.nvidia.com/compute/cuda/repos/debian11/x86_64/cuda-keyring_1.0-1_all.deb && \
    dpkg -i cuda-keyring_1.0-1_all.deb && \
    rm -f cuda-keyring_1.0-1_all.deb && \
    apt-get update && \
    apt-get install -y cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} libcusparse-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} libcusolver-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}  && apt-get clean \
    ; fi
ENV PATH /usr/local/cuda/bin:${PATH}

# OpenBLAS requirements and stable diffusion
RUN apt-get install -y \
    libopenblas-dev \
    libopencv-dev \ 
    && apt-get clean

# Set up OpenCV
RUN ln -s /usr/include/opencv4/opencv2 /usr/include/opencv2

WORKDIR /build

RUN test -n "$TARGETARCH" \
    || (echo 'warn: missing $TARGETARCH, either set this `ARG` manually, or run using `docker buildkit`')

# Extras requirements
FROM requirements-core as requirements-extras

RUN curl https://repo.anaconda.com/pkgs/misc/gpgkeys/anaconda.asc | gpg --dearmor > conda.gpg && \
    install -o root -g root -m 644 conda.gpg /usr/share/keyrings/conda-archive-keyring.gpg && \
    gpg --keyring /usr/share/keyrings/conda-archive-keyring.gpg --no-default-keyring --fingerprint 34161F5BF5EB1D4BFBBB8F0A8AEB4F8B29D82806 && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/conda-archive-keyring.gpg] https://repo.anaconda.com/pkgs/misc/debrepo/conda stable main" > /etc/apt/sources.list.d/conda.list && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/conda-archive-keyring.gpg] https://repo.anaconda.com/pkgs/misc/debrepo/conda stable main" | tee -a /etc/apt/sources.list.d/conda.list && \
    apt-get update && \
    apt-get install -y conda

ENV PATH="/root/.cargo/bin:${PATH}"
RUN pip install --upgrade pip
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y


# \
#    ; fi

###################################
###################################

FROM requirements-${IMAGE_TYPE} as builder

ARG GO_TAGS="stablediffusion tts"
ARG GRPC_BACKENDS
ARG BUILD_GRPC=true
ENV GRPC_BACKENDS=${GRPC_BACKENDS}
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

# stablediffusion does not tolerate a newer version of abseil, build it first
RUN GRPC_BACKENDS=backend-assets/grpc/stablediffusion make build

RUN if [ "${BUILD_GRPC}" = "true" ]; then \
    git clone --recurse-submodules -b v1.58.0 --depth 1 --shallow-submodules https://github.com/grpc/grpc && \
    cd grpc && mkdir -p cmake/build && cd cmake/build && cmake -DgRPC_INSTALL=ON \
      -DgRPC_BUILD_TESTS=OFF \
       ../.. && make -j12 install && rm -rf grpc \
    ; fi

# Rebuild with defaults backends
RUN make build

###################################
###################################

FROM requirements-${IMAGE_TYPE}

ARG FFMPEG
ARG BUILD_TYPE
ARG TARGETARCH
ARG IMAGE_TYPE=extras

ENV BUILD_TYPE=${BUILD_TYPE}
ENV REBUILD=false
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz

ARG CUDA_MAJOR_VERSION=11
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

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

# Copy the binary
COPY --from=builder /build/local-ai ./

# Copy shared libraries for piper
COPY --from=builder /build/go-piper/piper/build/pi/lib/* /usr/lib/

# do not let stablediffusion rebuild (requires an older version of absl)
COPY --from=builder /build/backend-assets/grpc/stablediffusion ./backend-assets/grpc/stablediffusion

## Duplicated from Makefile to avoid having a big layer that's hard to push
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/autogptq \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/bark \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/diffusers \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/vllm \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/huggingface \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/vall-e-x \
    ; fi
RUN if [ "${IMAGE_TYPE}" = "extras" ]; then \
	PATH=$PATH:/opt/conda/bin make -C backend/python/exllama \
    ; fi

# Copy VALLE-X as it's not a real "lib"
RUN if [ -d /usr/lib/vall-e-x ]; then \
    cp -rfv /usr/lib/vall-e-x/* ./ ; \ 
    fi

# we also copy exllama libs over to resolve exllama import error
RUN if [ -d /usr/local/lib/python3.9/dist-packages/exllama ]; then \
        cp -rfv /usr/local/lib/python3.9/dist-packages/exllama backend/python/exllama/;\
    fi

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f $HEALTHCHECK_ENDPOINT || exit 1

EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]
