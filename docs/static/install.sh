#!/bin/sh
# This script installs LocalAI on Linux.
# It detects the current operating system architecture and installs the appropriate version of LocalAI.

# Usage:
#   curl ... | ENV_VAR=... sh -
#       or
#   ENV_VAR=... ./install.sh

set -e
set -o noglob
#set -x

# --- helper functions for logs ---
info()
{
    echo ' ' "$@"
}

warn()
{
    echo '[WARN] ' "$@" >&2
}

fatal()
{
    echo '[ERROR] ' "$@" >&2
    exit 1
}

# --- fatal if no systemd or openrc ---
verify_system() {
    if [ -x /sbin/openrc-run ]; then
        HAS_OPENRC=true
        return
    fi
    if [ -x /bin/systemctl ] || type systemctl > /dev/null 2>&1; then
        HAS_SYSTEMD=true
        return
    fi
    fatal 'Can not find systemd or openrc to use as a process supervisor for local-ai.'
}

TEMP_DIR=$(mktemp -d)
cleanup() { rm -rf $TEMP_DIR; }
trap cleanup EXIT

available() { command -v $1 >/dev/null; }
require() {
    local MISSING=''
    for TOOL in $*; do
        if ! available $TOOL; then
            MISSING="$MISSING $TOOL"
        fi
    done

    echo $MISSING
}

## VARIABLES

# DOCKER_INSTALL - set to "true" to install Docker images
# USE_AIO - set to "true" to install the all-in-one LocalAI image
PORT=${PORT:-8080}

docker_found=false
if available docker ; then
    info "Docker detected."
    docker_found=true
    if [ -z $DOCKER_INSTALL ]; then
        info "Docker detected and no installation method specified. Using Docker."
    fi
fi

DOCKER_INSTALL=${DOCKER_INSTALL:-$docker_found}
USE_AIO=${USE_AIO:-false}
API_KEY=${API_KEY:-}
CORE_IMAGES=${CORE_IMAGES:-false}
P2P_TOKEN=${P2P_TOKEN:-}
WORKER=${WORKER:-false}
FEDERATED=${FEDERATED:-false}
FEDERATED_SERVER=${FEDERATED_SERVER:-false}

# nprocs -1
if available nproc; then
    procs=$(nproc)
else
    procs=1
fi
THREADS=${THREADS:-$procs}
LATEST_VERSION=$(curl -s "https://api.github.com/repos/mudler/LocalAI/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
VERSION="${VERSION:-$LATEST_VERSION}"
MODELS_PATH=${MODELS_PATH:-/usr/share/local-ai/models}


check_gpu() {
    # Look for devices based on vendor ID for NVIDIA and AMD
    case $1 in
        lspci)
            case $2 in
                nvidia) available lspci && lspci -d '10de:' | grep -q 'NVIDIA' || return 1 ;;
                amdgpu) available lspci && lspci -d '1002:' | grep -q 'AMD' || return 1 ;;
                intel) available lspci && lspci | grep -E 'VGA|3D' | grep -iq intel | return 1 ;;
            esac ;;
        lshw)
            case $2 in
                nvidia) available lshw && $SUDO lshw -c display -numeric | grep -q 'vendor: .* \[10DE\]' || return 1 ;;
                amdgpu) available lshw && $SUDO lshw -c display -numeric | grep -q 'vendor: .* \[1002\]' || return 1 ;;
                intel) available lshw  && $SUDO lshw -c display -numeric | grep -q 'vendor: .* \[8086\]' || return 1 ;;
            esac ;;
        nvidia-smi) available nvidia-smi || return 1 ;;
    esac
}


install_success() {
    info "The LocalAI API is now available at 127.0.0.1:$PORT."
    if [ "$DOCKER_INSTALL" = "true" ]; then
        info "The LocalAI Docker container is now running."
    else
        info 'Install complete. Run "local-ai" from the command line.'
    fi
}

aborted() {
    warn 'Installation aborted.'
    exit 1
}

trap aborted INT

configure_systemd() {
    if ! id local-ai >/dev/null 2>&1; then
        info "Creating local-ai user..."
        $SUDO useradd -r -s /bin/false -U -m -d /usr/share/local-ai local-ai
    fi

    info "Adding current user to local-ai group..."
    $SUDO usermod -a -G local-ai $(whoami)
    info "Creating local-ai systemd service..."
    cat <<EOF | $SUDO tee /etc/systemd/system/local-ai.service >/dev/null
[Unit]
Description=LocalAI Service
After=network-online.target

[Service]
ExecStart=$BINDIR/local-ai $STARTCOMMAND
User=local-ai
Group=local-ai
Restart=always
EnvironmentFile=/etc/localai.env
RestartSec=3
Environment="PATH=$PATH"
WorkingDirectory=/usr/share/local-ai

[Install]
WantedBy=default.target
EOF
    
    $SUDO touch /etc/localai.env
    $SUDO echo "ADDRESS=0.0.0.0:$PORT" | $SUDO tee /etc/localai.env >/dev/null
    $SUDO echo "API_KEY=$API_KEY" | $SUDO tee -a /etc/localai.env >/dev/null
    $SUDO echo "THREADS=$THREADS" | $SUDO tee -a /etc/localai.env >/dev/null
    $SUDO echo "MODELS_PATH=$MODELS_PATH" | $SUDO tee -a /etc/localai.env >/dev/null

    if [ -n "$P2P_TOKEN" ]; then
        $SUDO echo "LOCALAI_P2P_TOKEN=$P2P_TOKEN" | $SUDO tee -a /etc/localai.env >/dev/null
        $SUDO echo "LOCALAI_P2P=true" | $SUDO tee -a /etc/localai.env >/dev/null
    fi

    if [ "$LOCALAI_P2P_DISABLE_DHT" = true ]; then
        $SUDO echo "LOCALAI_P2P_DISABLE_DHT=true" | $SUDO tee -a /etc/localai.env >/dev/null
    fi

    SYSTEMCTL_RUNNING="$(systemctl is-system-running || true)"
    case $SYSTEMCTL_RUNNING in
        running|degraded)
            info "Enabling and starting local-ai service..."
            $SUDO systemctl daemon-reload
            $SUDO systemctl enable local-ai

            start_service() { $SUDO systemctl restart local-ai; }
            trap start_service EXIT
            ;;
    esac
}



# ref: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-yum-or-dnf
install_container_toolkit_yum() {
    info 'Installing NVIDIA repository...'

    curl -s -L https://nvidia.github.io/libnvidia-container/stable/rpm/nvidia-container-toolkit.repo | \
    $SUDO  tee /etc/yum.repos.d/nvidia-container-toolkit.repo

    if [ "$PACKAGE_MANAGER" = "dnf" ]; then
        $SUDO $PACKAGE_MANAGER config-manager --enable nvidia-container-toolkit-experimental
    else 
        $SUDO $PACKAGE_MANAGER -y install yum-utils
        $SUDO $PACKAGE_MANAGER-config-manager --enable nvidia-container-toolkit-experimental
    fi
    $SUDO $PACKAGE_MANAGER install -y nvidia-container-toolkit
}

# ref: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-apt
install_container_toolkit_apt() {
    info 'Installing NVIDIA repository...'

    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | $SUDO gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg \
  && curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
    sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
    $SUDO tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

    $SUDO sudo apt-get update && $SUDO apt-get install -y nvidia-container-toolkit
}

# ref: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-zypper
install_container_toolkit_zypper() {
    info 'Installing NVIDIA repository...'
    $SUDO zypper ar https://nvidia.github.io/libnvidia-container/stable/rpm/nvidia-container-toolkit.repo
    $SUDO zypper modifyrepo --enable nvidia-container-toolkit-experimental
    $SUDO zypper --gpg-auto-import-keys install -y nvidia-container-toolkit
}

install_container_toolkit() {
    if [ ! -f "/etc/os-release" ]; then
        fatal "Unknown distribution. Skipping CUDA installation."
    fi

    ## Check if it's already installed
    if check_gpu nvidia-smi && available nvidia-container-runtime; then
        info "NVIDIA Container Toolkit already installed."
        return
    fi

    . /etc/os-release

    OS_NAME=$ID
    OS_VERSION=$VERSION_ID

    info "Installing NVIDIA Container Toolkit..."
    case $OS_NAME in
            amzn|fedora|rocky|centos|rhel) install_container_toolkit_yum ;;
            debian|ubuntu) install_container_toolkit_apt ;;
            opensuse*|suse*) install_container_toolkit_zypper ;;
            *) echo "Could not install nvidia container toolkit - unknown OS" ;;
    esac
}

# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#rhel-7-centos-7
# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#rhel-8-rocky-8
# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#rhel-9-rocky-9
# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#fedora
install_cuda_driver_yum() {
    info 'Installing NVIDIA repository...'
    case $PACKAGE_MANAGER in
        yum)
            $SUDO $PACKAGE_MANAGER -y install yum-utils
            $SUDO $PACKAGE_MANAGER-config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/$1$2/$(uname -m)/cuda-$1$2.repo
            ;;
        dnf)
            $SUDO $PACKAGE_MANAGER config-manager --add-repo https://developer.download.nvidia.com/compute/cuda/repos/$1$2/$(uname -m)/cuda-$1$2.repo
            ;;
    esac

    case $1 in
        rhel)
            info 'Installing EPEL repository...'
            # EPEL is required for third-party dependencies such as dkms and libvdpau
            $SUDO $PACKAGE_MANAGER -y install https://dl.fedoraproject.org/pub/epel/epel-release-latest-$2.noarch.rpm || true
            ;;
    esac

    info 'Installing CUDA driver...'

    if [ "$1" = 'centos' ] || [ "$1$2" = 'rhel7' ]; then
        $SUDO $PACKAGE_MANAGER -y install nvidia-driver-latest-dkms
    fi

    $SUDO $PACKAGE_MANAGER -y install cuda-drivers
}

# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#ubuntu
# ref: https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#debian
install_cuda_driver_apt() {
    info 'Installing NVIDIA repository...'
    curl -fsSL -o $TEMP_DIR/cuda-keyring.deb https://developer.download.nvidia.com/compute/cuda/repos/$1$2/$(uname -m)/cuda-keyring_1.1-1_all.deb

    case $1 in
        debian)
            info 'Enabling contrib sources...'
            $SUDO sed 's/main/contrib/' < /etc/apt/sources.list | $SUDO tee /etc/apt/sources.list.d/contrib.list > /dev/null
            if [ -f "/etc/apt/sources.list.d/debian.sources" ]; then
                $SUDO sed 's/main/contrib/' < /etc/apt/sources.list.d/debian.sources | $SUDO tee /etc/apt/sources.list.d/contrib.sources > /dev/null
            fi
            ;;
    esac

    info 'Installing CUDA driver...'
    $SUDO dpkg -i $TEMP_DIR/cuda-keyring.deb
    $SUDO apt-get update

    [ -n "$SUDO" ] && SUDO_E="$SUDO -E" || SUDO_E=
    DEBIAN_FRONTEND=noninteractive $SUDO_E apt-get -y install cuda-drivers -q
}

install_cuda() {
    if [ ! -f "/etc/os-release" ]; then
        fatal "Unknown distribution. Skipping CUDA installation."
    fi

    . /etc/os-release

    OS_NAME=$ID
    OS_VERSION=$VERSION_ID

    if [ -z "$PACKAGE_MANAGER" ]; then
        fatal "Unknown package manager. Skipping CUDA installation."
    fi

    if ! check_gpu nvidia-smi || [ -z "$(nvidia-smi | grep -o "CUDA Version: [0-9]*\.[0-9]*")" ]; then
        case $OS_NAME in
            centos|rhel) install_cuda_driver_yum 'rhel' $(echo $OS_VERSION | cut -d '.' -f 1) ;;
            rocky) install_cuda_driver_yum 'rhel' $(echo $OS_VERSION | cut -c1) ;;
            fedora) [ $OS_VERSION -lt '37' ] && install_cuda_driver_yum $OS_NAME $OS_VERSION || install_cuda_driver_yum $OS_NAME '37';;
            amzn) install_cuda_driver_yum 'fedora' '37' ;;
            debian) install_cuda_driver_apt $OS_NAME $OS_VERSION ;;
            ubuntu) install_cuda_driver_apt $OS_NAME $(echo $OS_VERSION | sed 's/\.//') ;;
            *) exit ;;
        esac
    fi

    if ! lsmod | grep -q nvidia || ! lsmod | grep -q nvidia_uvm; then
        KERNEL_RELEASE="$(uname -r)"
        case $OS_NAME in
            rocky) $SUDO $PACKAGE_MANAGER -y install kernel-devel kernel-headers ;;
            centos|rhel|amzn) $SUDO $PACKAGE_MANAGER -y install kernel-devel-$KERNEL_RELEASE kernel-headers-$KERNEL_RELEASE ;;
            fedora) $SUDO $PACKAGE_MANAGER -y install kernel-devel-$KERNEL_RELEASE ;;
            debian|ubuntu) $SUDO apt-get -y install linux-headers-$KERNEL_RELEASE ;;
            *) exit ;;
        esac

        NVIDIA_CUDA_VERSION=$($SUDO dkms info | awk -F: '/added/ { print $1 }')
        if [ -n "$NVIDIA_CUDA_VERSION" ]; then
            $SUDO dkms install $NVIDIA_CUDA_VERSION
        fi

        if lsmod | grep -q nouveau; then
            info 'Reboot to complete NVIDIA CUDA driver install.'
            exit 0
        fi

        $SUDO modprobe nvidia
        $SUDO modprobe nvidia_uvm
    fi

    # make sure the NVIDIA modules are loaded on boot with nvidia-persistenced
    if command -v nvidia-persistenced > /dev/null 2>&1; then
        $SUDO touch /etc/modules-load.d/nvidia.conf
        MODULES="nvidia nvidia-uvm"
        for MODULE in $MODULES; do
            if ! grep -qxF "$MODULE" /etc/modules-load.d/nvidia.conf; then
                echo "$MODULE" | sudo tee -a /etc/modules-load.d/nvidia.conf > /dev/null
            fi
        done
    fi

    info "NVIDIA GPU ready."
    install_success

}

install_amd() {
    # Look for pre-existing ROCm v6 before downloading the dependencies
    for search in "${HIP_PATH:-''}" "${ROCM_PATH:-''}" "/opt/rocm" "/usr/lib64"; do
        if [ -n "${search}" ] && [ -e "${search}/libhipblas.so.2" -o -e "${search}/lib/libhipblas.so.2" ]; then
            info "Compatible AMD GPU ROCm library detected at ${search}"
            install_success
            exit 0
        fi
    done

    info "AMD GPU ready."
    exit 0
}

install_docker() {
    [ "$(uname -s)" = "Linux" ] || fatal 'This script is intended to run on Linux only.'

    if ! available docker; then
        info "Installing Docker..."
        curl -fsSL https://get.docker.com | sh
    fi

    # Check docker is running
    if ! $SUDO systemctl is-active --quiet docker; then
        info "Starting Docker..."
        $SUDO systemctl start docker
    fi

    info "Starting LocalAI Docker container..."
    # Create volume if doesn't exist already
    if ! $SUDO docker volume inspect local-ai-data > /dev/null 2>&1; then
        $SUDO docker volume create local-ai-data
    fi

    # Check if container is already runnning
    if $SUDO docker ps -a --format '{{.Names}}' | grep -q local-ai; then
        info "LocalAI Docker container already exists, replacing it..."
        $SUDO docker rm -f local-ai
        # # Check if it is running
        # if $SUDO docker ps --format '{{.Names}}' | grep -q local-ai; then
        #     info "LocalAI Docker container is already running."
        #     exit 0
        # fi 

        # info "Starting LocalAI Docker container..."
        # $SUDO docker start local-ai
        # exit 0
    fi

    envs=""
    if [ -n "$P2P_TOKEN" ]; then
        envs="-e LOCALAI_P2P_TOKEN=$P2P_TOKEN -e LOCALAI_P2P=true"
    fi
    if [ "$LOCALAI_P2P_DISABLE_DHT" = true ]; then
        envs="$envs -e LOCALAI_P2P_DISABLE_DHT=true"
    fi

    IMAGE_TAG=
    if [ "$HAS_CUDA" ]; then
        IMAGE_TAG=${VERSION}-cublas-cuda12-ffmpeg
        # CORE
        if [ "$CORE_IMAGES" = true ]; then
            IMAGE_TAG=${VERSION}-cublas-cuda12-ffmpeg-core
        fi
        # AIO
        if [ "$USE_AIO" = true ]; then
            IMAGE_TAG=${VERSION}-aio-gpu-nvidia-cuda-12
        fi

        if ! available nvidia-smi; then
            info "Installing nvidia-cuda-toolkit..."
            # TODO:
            $SUDO apt-get -y install nvidia-cuda-toolkit
        fi

        $SUDO docker run -v local-ai-data:/build/models \
            --gpus all \
            --restart=always \
            -e API_KEY=$API_KEY \
            -e THREADS=$THREADS \
            $envs \
            -d -p $PORT:8080 --name local-ai localai/localai:$IMAGE_TAG $STARTCOMMAND
    elif [ "$HAS_AMD" ]; then
        IMAGE_TAG=${VERSION}-hipblas-ffmpeg
        # CORE
        if [ "$CORE_IMAGES" = true ]; then
            IMAGE_TAG=${VERSION}-hipblas-ffmpeg-core
        fi
        # AIO
        if [ "$USE_AIO" = true ]; then
            IMAGE_TAG=${VERSION}-aio-gpu-hipblas
        fi

        $SUDO docker run -v local-ai-data:/build/models \
            --device /dev/dri \
            --device /dev/kfd \
            --restart=always \
            -e API_KEY=$API_KEY \
            -e THREADS=$THREADS \
            $envs \
            -d -p $PORT:8080 --name local-ai localai/localai:$IMAGE_TAG $STARTCOMMAND
    elif [ "$HAS_INTEL" ]; then
        IMAGE_TAG=${VERSION}-sycl-f32-ffmpeg
        # CORE
        if [ "$CORE_IMAGES" = true ]; then
            IMAGE_TAG=${VERSION}-sycl-f32-ffmpeg-core
        fi
        # AIO
        if [ "$USE_AIO" = true ]; then
            IMAGE_TAG=${VERSION}-aio-gpu-intel-f32
        fi

        $SUDO docker run -v local-ai-data:/build/models \
            --device /dev/dri \
            --restart=always \
            -e API_KEY=$API_KEY \
            -e THREADS=$THREADS \
            $envs \
            -d -p $PORT:8080 --name local-ai localai/localai:$IMAGE_TAG $STARTCOMMAND
    else
        IMAGE_TAG=${VERSION}-ffmpeg
        # CORE
        if [ "$CORE_IMAGES" = true ]; then
            IMAGE_TAG=${VERSION}-ffmpeg-core
        fi
        # AIO
        if [ "$USE_AIO" = true ]; then
            IMAGE_TAG=${VERSION}-aio-cpu
        fi        
        $SUDO docker run -v local-ai-data:/models \
                --restart=always \
                -e MODELS_PATH=/models \
                -e API_KEY=$API_KEY \
                -e THREADS=$THREADS \
                $envs \
                -d -p $PORT:8080 --name local-ai localai/localai:$IMAGE_TAG $STARTCOMMAND
    fi

    install_success
    exit 0
}

install_binary_darwin() {
    [ "$(uname -s)" = "Darwin" ] || fatal 'This script is intended to run on macOS only.'

    info "Downloading local-ai..."
    curl --fail --show-error --location --progress-bar -o $TEMP_DIR/local-ai "https://github.com/mudler/LocalAI/releases/download/${VERSION}/local-ai-Darwin-${ARCH}"

    info "Installing local-ai..."
    install -o0 -g0 -m755 $TEMP_DIR/local-ai /usr/local/bin/local-ai

    install_success
}

install_binary() {
    [ "$(uname -s)" = "Linux" ] || fatal 'This script is intended to run on Linux only.'


    IS_WSL2=false

    KERN=$(uname -r)
    case "$KERN" in
        *icrosoft*WSL2 | *icrosoft*wsl2) IS_WSL2=true;;
        *icrosoft) fatal "Microsoft WSL1 is not currently supported. Please upgrade to WSL2 with 'wsl --set-version <distro> 2'" ;;
        *) ;;
    esac


    NEEDS=$(require curl awk grep sed tee xargs)
    if [ -n "$NEEDS" ]; then
        info "ERROR: The following tools are required but missing:"
        for NEED in $NEEDS; do
            echo "  - $NEED"
        done
        exit 1
    fi

    info "Downloading local-ai..."
    curl --fail --location --progress-bar -o $TEMP_DIR/local-ai "https://github.com/mudler/LocalAI/releases/download/${VERSION}/local-ai-Linux-${ARCH}"

    for BINDIR in /usr/local/bin /usr/bin /bin; do
        echo $PATH | grep -q $BINDIR && break || continue
    done

    info "Installing local-ai to $BINDIR..."
    $SUDO install -o0 -g0 -m755 -d $BINDIR
    $SUDO install -o0 -g0 -m755 $TEMP_DIR/local-ai $BINDIR/local-ai

    verify_system
    if [ "$HAS_SYSTEMD" = true ]; then
        configure_systemd
    fi

    # WSL2 only supports GPUs via nvidia passthrough
    # so check for nvidia-smi to determine if GPU is available
    if [ "$IS_WSL2" = true ]; then
        if available nvidia-smi && [ -n "$(nvidia-smi | grep -o "CUDA Version: [0-9]*\.[0-9]*")" ]; then
            info "Nvidia GPU detected."
        fi
        install_success
        exit 0
    fi

    # Install GPU dependencies on Linux
    if ! available lspci && ! available lshw; then
        warn "Unable to detect NVIDIA/AMD GPU. Install lspci or lshw to automatically detect and install GPU dependencies."
        exit 0
    fi

    if [ "$HAS_AMD" = true ]; then
        install_amd
    fi

    if [ "$HAS_CUDA" = true ]; then
        if check_gpu nvidia-smi; then
            info "NVIDIA GPU installed."
            exit 0
        fi

        install_cuda
    fi

    install_success
    warn "No NVIDIA/AMD GPU detected. LocalAI will run in CPU-only mode."
    exit 0
}

detect_start_command() {
    STARTCOMMAND="run"
    if [ "$WORKER" = true ]; then
        if [ -n "$P2P_TOKEN" ]; then
            STARTCOMMAND="worker p2p-llama-cpp-rpc"
        else 
            STARTCOMMAND="worker llama-cpp-rpc"
        fi
    elif [ "$FEDERATED" = true ]; then
        if [ "$FEDERATED_SERVER" = true ]; then
            STARTCOMMAND="federated"
        else
            STARTCOMMAND="$STARTCOMMAND --p2p --federated"
        fi
    elif [ -n "$P2P_TOKEN" ]; then
        STARTCOMMAND="$STARTCOMMAND --p2p"
    fi
}


detect_start_command

OS="$(uname -s)"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64) ARCH="x86_64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) fatal "Unsupported architecture: $ARCH" ;;
esac

if [ "$OS" = "Darwin" ]; then
    install_binary_darwin
    exit 0
fi

if check_gpu lspci amdgpu || check_gpu lshw amdgpu; then
    HAS_AMD=true
fi

if check_gpu lspci nvidia || check_gpu lshw nvidia; then
    HAS_CUDA=true
fi

if check_gpu lspci intel || check_gpu lshw intel; then
    HAS_INTEL=true
fi

SUDO=
if [ "$(id -u)" -ne 0 ]; then
    # Running as root, no need for sudo
    if ! available sudo; then
        fatal "This script requires superuser permissions. Please re-run as root."
    fi

    SUDO="sudo"
fi

PACKAGE_MANAGER=
for PACKAGE_MANAGER in dnf yum apt-get; do
    if available $PACKAGE_MANAGER; then
        break
    fi
done

if [ "$DOCKER_INSTALL" = "true" ]; then
    if [ "$HAS_CUDA" = true ]; then
        install_container_toolkit
    fi
    install_docker
else
    install_binary
fi
