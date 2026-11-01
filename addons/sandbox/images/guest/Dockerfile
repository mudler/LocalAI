# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
#
# Guest image of the Sandbox add-on: a Debian userland, the guest
# kernel at /boot/vmlinux and the guest agent at /sbin/sandbox-agent. Build
# it from the repository root:
#
#   docker buildx build -f addons/sandbox/images/guest/Dockerfile .
#
# The add-on is built with the digest of the result in main.guestImage.

ARG DEBIAN=debian:trixie-slim@sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f
ARG GOLANG=golang:1.26-trixie@sha256:f06b7122f888680048afb247c4e5f49c0069eb6c4c1ccae0f3f4834527a8d22c

# The kernel builds on the build machine and cross-compiles for the target.
FROM --platform=$BUILDPLATFORM ${DEBIAN} AS kernel
ARG KERNEL_VERSION=6.18.57
ARG KERNEL_SHA256=5bd9acdc82ec5efc3790ae840e5aaa40dfde7efff261cf6dde902dc570acf98c
ARG BUILDARCH
ARG TARGETARCH
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) cross=gcc-x86-64-linux-gnu ;; \
      arm64) cross=gcc-aarch64-linux-gnu ;; \
      *) echo "unsupported architecture ${TARGETARCH}"; exit 1 ;; \
    esac; \
    [ "${TARGETARCH}" = "${BUILDARCH}" ] && cross=; \
    apt-get update; \
    apt-get install -y --no-install-recommends bc bison build-essential ca-certificates cpio curl flex libelf-dev libssl-dev python3 xz-utils ${cross}; \
    rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN set -eux; \
    curl -fsSLo /linux.tar.xz "https://cdn.kernel.org/pub/linux/kernel/v${KERNEL_VERSION%%.*}.x/linux-${KERNEL_VERSION}.tar.xz"; \
    echo "${KERNEL_SHA256}  /linux.tar.xz" | sha256sum -c -; \
    tar -xf /linux.tar.xz --strip-components=1; \
    rm /linux.tar.xz
COPY addons/sandbox/images/guest/kernel.config addons/sandbox/images/guest/kernel-x86_64.config addons/sandbox/images/guest/kernel-arm64.config /config/
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) arch=x86_64; image=vmlinux; prefix=x86_64-linux-gnu- ;; \
      arm64) arch=arm64; image=arch/arm64/boot/Image; prefix=aarch64-linux-gnu- ;; \
    esac; \
    [ "${TARGETARCH}" = "${BUILDARCH}" ] && prefix=; \
    make ARCH=$arch CROSS_COMPILE=$prefix defconfig; \
    scripts/kconfig/merge_config.sh -m .config /config/kernel.config /config/kernel-$arch.config; \
    make ARCH=$arch CROSS_COMPILE=$prefix olddefconfig; \
    for f in /config/kernel.config /config/kernel-$arch.config; do \
      grep -E '^(CONFIG_[A-Z0-9_]+=y|# CONFIG_[A-Z0-9_]+ is not set)$' "$f" | while read -r line; do \
        if ! grep -qxF "$line" .config; then \
          case "$line" in \
            "# "*) sym=${line#\# }; sym=${sym% is not set}; ! grep -q "^$sym=" .config || { echo "kernel option still set: $line"; exit 1; } ;; \
            *) echo "kernel option missing: $line"; exit 1 ;; \
          esac; \
        fi; \
      done; \
    done; \
    make ARCH=$arch CROSS_COMPILE=$prefix -j"$(nproc)" "$(basename $image)"; \
    cp "$image" /vmlinux

# "--target vmlinux --output type=local,dest=<dir>" exports only the kernel.
FROM scratch AS vmlinux
COPY --from=kernel /vmlinux /vmlinux

# The guest agent is pure Go.
FROM --platform=$BUILDPLATFORM ${GOLANG} AS agent
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY addons/sandbox/internal/sandbox/guestagent ./addons/sandbox/internal/sandbox/guestagent
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /sandbox-agent ./addons/sandbox/internal/sandbox/guestagent/cmd/sandbox-agent

FROM ${DEBIAN}
ARG IMAGE_VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/mudler/LocalAI" \
      org.opencontainers.image.version="${IMAGE_VERSION}" \
      org.opencontainers.image.description="Guest image of the LocalAI Sandbox add-on"
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends bash ca-certificates curl file git jq poppler-utils python3 python3-pip \
      python3-pypdf python3-pandas python3-pil ripgrep sqlite3 tree unzip zip; \
    rm -rf /var/lib/apt/lists/*; \
    groupadd --gid 1000 agent; \
    useradd --uid 1000 --gid 1000 --create-home --home-dir /home/agent --shell /bin/bash agent; \
    mkdir -p /workspace
# The Paper office libraries and the PDF libraries, pinned with hashes. The
# Paper packages import as docx, openpyxl and pptx, so the upstream packages
# python-docx, openpyxl and python-pptx must not be installed next to them.
# This runs on the build machine's network, before the guest proxy below. The
# check fails the build when a library is missing or comes from the wrong
# distribution, and check-tools.sh fails it when a command-line tool (Poppler,
# jq, zip, unzip, file, sqlite3, ripgrep, tree) is missing or not usable. The pip
# wheels go to /usr/local and shadow the Debian copies of the same libraries,
# so "pip check" fails the build when the two sets do not fit together. The
# apt package python3-pil stays on purpose: the pinned Pillow wheel shadows
# it, and the Debian copy is the fallback if the wheel is ever removed.
COPY addons/sandbox/images/guest/python-requirements.txt addons/sandbox/images/guest/check-python.py addons/sandbox/images/guest/check-tools.sh /tmp/guest-python/
RUN set -eux; \
    PIP_BREAK_SYSTEM_PACKAGES=1 python3 -m pip install --no-cache-dir --no-deps --only-binary=:all: --require-hashes \
      -r /tmp/guest-python/python-requirements.txt; \
    python3 -m pip check; \
    python3 /tmp/guest-python/check-python.py; \
    sh /tmp/guest-python/check-tools.sh; \
    rm -rf /tmp/guest-python
# The guest has no network device. Every tool must use the proxy on the
# loopback interface; the sandbox serves it with a host port handler.
ENV HTTP_PROXY=http://127.0.0.1:3128 HTTPS_PROXY=http://127.0.0.1:3128 NO_PROXY=localhost,127.0.0.1 \
    http_proxy=http://127.0.0.1:3128 https_proxy=http://127.0.0.1:3128 no_proxy=localhost,127.0.0.1
# Go and npm fetch from their registries through the proxy, not from version
# control hosts directly (GOPROXY without ",direct"). cargo and uv read
# HTTPS_PROXY.
ENV GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod \
    npm_config_proxy=http://127.0.0.1:3128 npm_config_https_proxy=http://127.0.0.1:3128
RUN set -eux; \
    printf '[global]\nproxy = http://127.0.0.1:3128\nbreak-system-packages = true\n' > /etc/pip.conf; \
    printf 'Acquire::http::Proxy "http://127.0.0.1:3128";\nAcquire::https::Proxy "http://127.0.0.1:3128";\n' > /etc/apt/apt.conf.d/80proxy; \
    git config --system http.proxy http://127.0.0.1:3128
COPY --from=kernel /vmlinux /boot/vmlinux
COPY --from=agent /sandbox-agent /sbin/sandbox-agent
