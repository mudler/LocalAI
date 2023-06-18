#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Syntax: ./cu-blas.sh [KEYRING_VERSION] [CUDA_MAJOR_VERSION] [CUDA_MINOR_VERSION]

set -e

BUILD_TYPE=${1:-"none"}
KEYRING_VERSION=${2:-"1.0-1"}
CUDA_MAJOR_VERSION=${3:-"11"}
CUDA_MINOR_VERSION=${4:-"7"}


export DEBIAN_FRONTEND=noninteractive

# Install software-properties-common
if [ "${BUILD_TYPE}" = "cublas" ] && ! dpkg -s software-properties-common > /dev/null 2>&1; then
    if [ ! -d "/var/lib/apt/lists" ] || [ "$(ls /var/lib/apt/lists/ | wc -l)" = "0" ]; then
        apt-get update
    fi
    apt-get -y install --no-install-recommends software-properties-common
fi

# Install cuda-keyring
CUDA_KEYRING_SCRIPT="$(cat <<EOF
    set -e
    # Wrapper function to only use sudo if not already root
    sudoIf()
    {
        if [ "\$(id -u)" -ne 0 ]; then
            sudo "\$@"
        else
            "\$@"
        fi
    }
    sudoIf apt-add-repository contrib
    echo "Downloading Cuda-Keyring ${KEYRING_VERSION}..."
    curl -sSL -o /tmp/cuda-keyring_${KEYRING_VERSION}_all.deb "https://developer.download.nvidia.com/compute/cuda/repos/debian11/x86_64/cuda-keyring_${KEYRING_VERSION}_all.deb"
    dpkg -i cuda-keyring_${KEYRING_VERSION}_all.deb
    sudoIf apt-get update
    sudoIf apt-get -y install --no-install-recommends cuda-nvcc-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION} libcublas-dev-${CUDA_MAJOR_VERSION}-${CUDA_MINOR_VERSION}
EOF
)"

if [ "${BUILD_TYPE}" = "cublas" ] > /dev/null 2>&1; then
    "${SCHEME_INSTALL_SCRIPT}"
else
    echo "Cuda keyring already installed. Skipping."
fi

echo "Done!"