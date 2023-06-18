ARG GO_VERSION=1.20-bullseye
FROM golang:$GO_VERSION as requirements

# [Option] Upgrade OS packages to their latest versions
ARG UPGRADE_PACKAGES="$true"

COPY scripts/debian.sh /tmp/library-scripts/
RUN bash /tmp/library-scripts/debian.sh "${UPGRADE_PACKAGES}" \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/* /tmp/library-scripts

# Install CuBLAS
ARG BUILD_TYPE
ARG KEYRING_VERSION="1.0-1"
ARG CUDA_MAJOR_VERSION="11"
ARG CUDA_MINOR_VERSION="7"

# ENV BUILD_TYPE=${BUILD_TYPE}
COPY scripts/cu-blas.sh /tmp/library-scripts/
RUN bash /tmp/library-scripts/cu-blas.sh "${BUILD_TYPE}" "${KEYRING_VERSION}" "${CUDA_MAJOR_VERSION} ${CUDA_MINOR_VERSION}" \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/* /tmp/library-scripts
ENV PATH /usr/local/cuda/bin:${PATH}

# Install OpenBLAS 
COPY scripts/open-blas.sh /tmp/library-scripts/
RUN bash /tmp/library-scripts/open-blas.sh \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/* /tmp/library-scripts

# Install Stable Diffusion requirements
COPY scripts/stable-diffusion.sh /tmp/library-scripts/
RUN bash /tmp/library-scripts/stable-diffusion.sh \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/* /tmp/library-scripts


FROM requirements as builder

ARG GO_TAGS=stablediffusion

ENV GO_TAGS=${GO_TAGS}
ENV NVIDIA_DRIVER_CAPABILITIES=compute,utility
ENV NVIDIA_REQUIRE_CUDA="cuda>=${CUDA_MAJOR_VERSION}.0"
ENV NVIDIA_VISIBLE_DEVICES=all

WORKDIR /build

COPY . .
RUN make build


FROM requirements

ARG FFMPEG

ENV REBUILD=true
ENV HEALTHCHECK_ENDPOINT=http://localhost:8080/readyz

# Install FFmpeg
COPY scripts/ffmpeg.sh /tmp/library-scripts/
RUN bash /tmp/library-scripts/ffmpeg.sh "${FFMPEG}" \
    && apt-get clean -y && rm -rf /var/lib/apt/lists/* /tmp/library-scripts

WORKDIR /build

COPY . .
RUN make prepare-sources
COPY --from=builder /build/local-ai ./

# Define the health check command
HEALTHCHECK --interval=1m --timeout=10m --retries=10 \
  CMD curl -f $HEALTHCHECK_ENDPOINT || exit 1

EXPOSE 8080
ENTRYPOINT [ "/build/entrypoint.sh" ]
