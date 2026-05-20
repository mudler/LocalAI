#!/usr/bin/env bash
# Single source of truth for builder-base contents.
#
# Used by:
#   - backend/Dockerfile.base-grpc-builder        (CI prebuilt-base source of truth)
#   - backend/Dockerfile.llama-cpp                (builder-fromsource stage)
#   - backend/Dockerfile.ik-llama-cpp             (builder-fromsource stage)
#   - backend/Dockerfile.turboquant               (builder-fromsource stage)
#
# All four files invoke this script via
#   RUN --mount=type=bind,source=.docker/install-base-deps.sh,target=/usr/local/sbin/install-base-deps \
#       --mount=type=bind,source=.docker/apt-mirror.sh,target=/usr/local/sbin/apt-mirror \
#       bash /usr/local/sbin/install-base-deps
#
# so the prebuilt CI base image and the from-source local-dev path are
# bit-equivalent by construction.
#
# Inputs (env, populated from Dockerfile ARG/ENV):
#   BUILD_TYPE                ("cublas"|"l4t"|"hipblas"|"vulkan"|"sycl"|"clblas"|"")
#   CUDA_MAJOR_VERSION        ("12" | "13" | "")
#   CUDA_MINOR_VERSION        ("8" | "0" | "")
#   TARGETARCH                ("amd64" | "arm64")
#   UBUNTU_VERSION            ("2204" | "2404")
#   SKIP_DRIVERS              ("false" | "true")
#   CMAKE_FROM_SOURCE         ("false" | "true")
#   CMAKE_VERSION             ("3.31.10")
#   GRPC_VERSION              ("v1.65.0")
#   GRPC_MAKEFLAGS            ("-j4 -Otarget")
#   APT_MIRROR / APT_PORTS_MIRROR  (optional; consumed by /usr/local/sbin/apt-mirror)
#   AMDGPU_TARGETS            (optional; only relevant for hipblas downstream)
#
# IMPORTANT: install logic is copied verbatim from the prior in-Dockerfile
# RUN blocks. Do not paraphrase apt invocations / version pins / sed line
# numbers / deb URLs — the bit-equivalence guarantee depends on it.

set -eux

# --- 0. apt mirror rewrite (no-op when APT_MIRROR / APT_PORTS_MIRROR unset) ---
if [ -x /usr/local/sbin/apt-mirror ]; then
    APT_MIRROR="${APT_MIRROR:-}" APT_PORTS_MIRROR="${APT_PORTS_MIRROR:-}" \
        sh /usr/local/sbin/apt-mirror
fi

export DEBIAN_FRONTEND=noninteractive
export MAKEFLAGS="${GRPC_MAKEFLAGS:-}"

# --- 1. Base apt build deps ---
apt-get update
apt-get install -y --no-install-recommends \
    build-essential \
    ccache git \
    ca-certificates \
    make \
    pkg-config libcurl4-openssl-dev \
    curl unzip \
    libssl-dev wget
apt-get clean
rm -rf /var/lib/apt/lists/*

# --- 2. Vulkan SDK (BUILD_TYPE=vulkan) ---
# NB: this block intentionally installs `cmake` via apt as part of the
# Vulkan tooling — must run before the dedicated CMake step below.
if [ "${BUILD_TYPE:-}" = "vulkan" ] && [ "${SKIP_DRIVERS:-false}" = "false" ]; then
    apt-get update
    apt-get install -y  --no-install-recommends \
        software-properties-common pciutils wget gpg-agent
    apt-get install -y libglm-dev cmake libxcb-dri3-0 libxcb-present0 libpciaccess0 \
        libpng-dev libxcb-keysyms1-dev libxcb-dri3-dev libx11-dev g++ gcc \
        libwayland-dev libxrandr-dev libxcb-randr0-dev libxcb-ewmh-dev \
        git python-is-python3 bison libx11-xcb-dev liblz4-dev libzstd-dev \
        ocaml-core ninja-build pkg-config libxml2-dev wayland-protocols python3-jsonschema \
        clang-format qtbase5-dev qt6-base-dev libxcb-glx0-dev sudo xz-utils
    if [ "amd64" = "${TARGETARCH:-}" ]; then
        wget "https://sdk.lunarg.com/sdk/download/1.4.335.0/linux/vulkansdk-linux-x86_64-1.4.335.0.tar.xz"
        tar -xf vulkansdk-linux-x86_64-1.4.335.0.tar.xz
        rm vulkansdk-linux-x86_64-1.4.335.0.tar.xz
        mkdir -p /opt/vulkan-sdk
        mv 1.4.335.0 /opt/vulkan-sdk/
        ( cd /opt/vulkan-sdk/1.4.335.0 && \
          ./vulkansdk --no-deps --maxjobs \
              vulkan-loader \
              vulkan-validationlayers \
              vulkan-extensionlayer \
              vulkan-tools \
              shaderc )
        cp -rfv /opt/vulkan-sdk/1.4.335.0/x86_64/bin/* /usr/bin/
        cp -rfv /opt/vulkan-sdk/1.4.335.0/x86_64/lib/* /usr/lib/x86_64-linux-gnu/
        cp -rfv /opt/vulkan-sdk/1.4.335.0/x86_64/include/* /usr/include/
        cp -rfv /opt/vulkan-sdk/1.4.335.0/x86_64/share/* /usr/share/
        rm -rf /opt/vulkan-sdk
    fi
    if [ "arm64" = "${TARGETARCH:-}" ]; then
        mkdir vulkan
        ( cd vulkan && \
          curl -L -o vulkan-sdk.tar.xz https://github.com/mudler/vulkan-sdk-arm/releases/download/1.4.335.0/vulkansdk-ubuntu-24.04-arm-1.4.335.0.tar.xz && \
          tar -xvf vulkan-sdk.tar.xz && \
          rm vulkan-sdk.tar.xz && \
          cd 1.4.335.0 && \
          cp -rfv aarch64/bin/* /usr/bin/ && \
          cp -rfv aarch64/lib/* /usr/lib/aarch64-linux-gnu/ && \
          cp -rfv aarch64/include/* /usr/include/ && \
          cp -rfv aarch64/share/* /usr/share/ )
        rm -rf vulkan
    fi
    ldconfig
    apt-get clean
    rm -rf /var/lib/apt/lists/*
fi

# --- 3. CUDA toolkit (BUILD_TYPE=cublas|l4t) ---
if { [ "${BUILD_TYPE:-}" = "cublas" ] || [ "${BUILD_TYPE:-}" = "l4t" ]; } && [ "${SKIP_DRIVERS:-false}" = "false" ]; then
    apt-get update
    apt-get install -y  --no-install-recommends \
        software-properties-common pciutils
    if [ "amd64" = "${TARGETARCH:-}" ]; then
        curl -O "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu${UBUNTU_VERSION}/x86_64/cuda-keyring_1.1-1_all.deb"
    fi
    if [ "arm64" = "${TARGETARCH:-}" ]; then
        if [ "${CUDA_MAJOR_VERSION}" = "13" ]; then
            curl -O "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu${UBUNTU_VERSION}/sbsa/cuda-keyring_1.1-1_all.deb"
        else
            curl -O "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu${UBUNTU_VERSION}/arm64/cuda-keyring_1.1-1_all.deb"
        fi
    fi
    dpkg -i cuda-keyring_1.1-1_all.deb
    rm -f cuda-keyring_1.1-1_all.deb
    apt-get update
    apt-get install -y --no-install-recommends \
        "cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
        "libcufft-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
        "libcurand-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
        "libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
        "libcusparse-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
        "libcusolver-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}"
    if [ "${CUDA_MAJOR_VERSION}" = "13" ] && [ "arm64" = "${TARGETARCH:-}" ]; then
        apt-get install -y --no-install-recommends \
            "libcufile-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
            "libcudnn9-cuda-${CUDA_MAJOR_VERSION}" \
            "cuda-cupti-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}" \
            "libnvjitlink-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}"
    fi
    apt-get clean
    rm -rf /var/lib/apt/lists/*
fi

# --- 4. cuDSS / NVPL on arm64 + cublas (legacy JetPack / Tegra) ---
# https://github.com/NVIDIA/Isaac-GR00T/issues/343
if [ "${BUILD_TYPE:-}" = "cublas" ] && [ "${TARGETARCH:-}" = "arm64" ]; then
    wget "https://developer.download.nvidia.com/compute/cudss/0.6.0/local_installers/cudss-local-tegra-repo-ubuntu${UBUNTU_VERSION}-0.6.0_0.6.0-1_arm64.deb"
    dpkg -i "cudss-local-tegra-repo-ubuntu${UBUNTU_VERSION}-0.6.0_0.6.0-1_arm64.deb"
    cp /var/cudss-local-tegra-repo-ubuntu"${UBUNTU_VERSION}"-0.6.0/cudss-*-keyring.gpg /usr/share/keyrings/
    apt-get update
    apt-get -y install cudss "cudss-cuda-${CUDA_MAJOR_VERSION}"
    wget "https://developer.download.nvidia.com/compute/nvpl/25.5/local_installers/nvpl-local-repo-ubuntu${UBUNTU_VERSION}-25.5_1.0-1_arm64.deb"
    dpkg -i "nvpl-local-repo-ubuntu${UBUNTU_VERSION}-25.5_1.0-1_arm64.deb"
    cp /var/nvpl-local-repo-ubuntu"${UBUNTU_VERSION}"-25.5/nvpl-*-keyring.gpg /usr/share/keyrings/
    apt-get update
    apt-get install -y nvpl
fi

# --- 5. clBLAS (BUILD_TYPE=clblas) ---
# Present in variant Dockerfiles' from-source path but not in master's
# Dockerfile.base-grpc-builder. No CI matrix entry currently uses this,
# but keep parity so a future BUILD_TYPE=clblas build doesn't drift.
if [ "${BUILD_TYPE:-}" = "clblas" ] && [ "${SKIP_DRIVERS:-false}" = "false" ]; then
    apt-get update
    apt-get install -y --no-install-recommends \
        libclblast-dev
    apt-get clean
    rm -rf /var/lib/apt/lists/*
fi

# --- 6. ROCm / HIP build deps (BUILD_TYPE=hipblas) ---
if [ "${BUILD_TYPE:-}" = "hipblas" ] && [ "${SKIP_DRIVERS:-false}" = "false" ]; then
    apt-get update
    apt-get install -y --no-install-recommends \
        hipblas-dev \
        hipblaslt-dev \
        rocblas-dev
    apt-get clean
    rm -rf /var/lib/apt/lists/*
    # I have no idea why, but the ROCM lib packages don't trigger ldconfig after they install,
    # which results in local-ai and others not being able to locate the libraries.
    # We run ldconfig ourselves to work around this packaging deficiency.
    ldconfig
    # Log which GPU architectures have rocBLAS kernel support
    echo "rocBLAS library data architectures:"
    (ls /opt/rocm*/lib/rocblas/library/Kernels* 2>/dev/null || ls /opt/rocm*/lib64/rocblas/library/Kernels* 2>/dev/null) | grep -oP 'gfx[0-9a-z+-]+' | sort -u || \
        echo "WARNING: No rocBLAS kernel data found"
fi

echo "TARGETARCH: ${TARGETARCH:-}"

# --- 7. protoc (always) ---
# The version in 22.04 is too old. We will create one as part of installing
# the GRPC build below but that will also bring in a newer version of absl
# which stablediffusion cannot compile with. This version of protoc is only
# here so that we can generate the grpc code for the stablediffusion build.
if [ "amd64" = "${TARGETARCH:-}" ]; then
    curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-x86_64.zip -o protoc.zip
    unzip -j -d /usr/local/bin protoc.zip bin/protoc
    rm protoc.zip
fi
if [ "arm64" = "${TARGETARCH:-}" ]; then
    curl -L -s https://github.com/protocolbuffers/protobuf/releases/download/v27.1/protoc-27.1-linux-aarch_64.zip -o protoc.zip
    unzip -j -d /usr/local/bin protoc.zip bin/protoc
    rm protoc.zip
fi

# --- 8. CMake (apt or compiled from source) ---
# The version in 22.04 is too old. Vulkan path above already pulled cmake
# via apt; the from-source branch here will install over it which is fine.
if [ "${CMAKE_FROM_SOURCE:-false}" = "true" ]; then
    curl -L -s "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}.tar.gz" -o cmake.tar.gz
    tar xvf cmake.tar.gz
    ( cd "cmake-${CMAKE_VERSION}" && ./configure && make && make install )
else
    apt-get update
    apt-get install -y \
        cmake
    apt-get clean
    rm -rf /var/lib/apt/lists/*
fi

# --- 9. gRPC compile + install at /opt/grpc ---
# We install GRPC to a different prefix here so that we can copy in only
# the build artifacts later — saves several hundred MB on the final docker
# image size vs copying in the entire GRPC source tree and running
# `make install` in the target container.
#
# The TESTONLY abseil sed patch and /opt/grpc prefix are load-bearing —
# downstream Dockerfiles `COPY` /opt/grpc to /usr/local (or rely on the
# prebuilt base having it at /opt/grpc).
mkdir -p /build
cd /build
git clone --recurse-submodules --jobs 4 -b "${GRPC_VERSION}" --depth 1 --shallow-submodules https://github.com/grpc/grpc
mkdir -p /build/grpc/cmake/build
cd /build/grpc/cmake/build
sed -i "216i\\  TESTONLY" "../../third_party/abseil-cpp/absl/container/CMakeLists.txt"
cmake -DgRPC_INSTALL=ON -DgRPC_BUILD_TESTS=OFF -DCMAKE_INSTALL_PREFIX:PATH=/opt/grpc ../..
make
make install
cd /
rm -rf /build
