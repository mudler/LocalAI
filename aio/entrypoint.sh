#!/bin/bash

echo "===> LocalAI All-in-One (AIO) container starting..."

GPU_ACCELERATION=false
GPU_VENDOR=""

function detect_gpu() {
    case "$(uname -s)" in
        Linux)
            if lspci | grep -E 'VGA|3D' | grep -iq nvidia; then
                echo "NVIDIA GPU detected"
                # nvidia-smi should be installed in the container
                if nvidia-smi; then
                    GPU_ACCELERATION=true
                    GPU_VENDOR=nvidia
                else
                    echo "NVIDIA GPU detected, but nvidia-smi is not installed. GPU acceleration will not be available."
                fi
            elif lspci | grep -E 'VGA|3D' | grep -iq amd; then
                echo "AMD GPU detected"
                # Check if ROCm is installed
                if [ -d /opt/rocm ]; then
                    GPU_ACCELERATION=true
                    GPU_VENDOR=amd
                else
                    echo "AMD GPU detected, but ROCm is not installed. GPU acceleration will not be available."
                fi
            elif lspci | grep -E 'VGA|3D' | grep -iq intel; then
                echo "Intel GPU detected"
                if [ -d /opt/intel ]; then
                    GPU_ACCELERATION=true
                else
                    echo "Intel GPU detected, but Intel GPU drivers are not installed. GPU acceleration will not be available."
                fi
            fi
            ;;
        Darwin)
            if system_profiler SPDisplaysDataType | grep -iq 'Metal'; then
                echo "Apple Metal supported GPU detected"
                GPU_ACCELERATION=true
                GPU_VENDOR=apple
            fi
            ;;
    esac
}

function detect_gpu_size() {
    if [ "$GPU_ACCELERATION" = true ]; then
        GPU_SIZE=gpu-8g
    fi

    # Attempting to find GPU memory size for NVIDIA GPUs
    if echo "$gpu_model" | grep -iq nvidia; then
        echo "NVIDIA GPU detected. Attempting to find memory size..."
        nvidia_sm=($(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits))
        if [ ! -z "$nvidia_sm" ]; then
            echo "Total GPU Memory: ${nvidia_sm[0]} MiB"
        else
            echo "Unable to determine NVIDIA GPU memory size."
        fi
        # if bigger than 8GB, use 16GB
        #if [ "$nvidia_sm" -gt 8192 ]; then
        #    GPU_SIZE=gpu-16g
        #fi
    else
        echo "Non-NVIDIA GPU detected. GPU memory size detection for non-NVIDIA GPUs is not supported in this script."
    fi

    # default to cpu if GPU_SIZE is not set
    if [ -z "$GPU_SIZE" ]; then
        GPU_SIZE=cpu
    fi
}

function check_vars() {
    if [ -z "$MODELS" ]; then
        echo "MODELS environment variable is not set. Please set it to a comma-separated list of model YAML files to load."
        exit 1
    fi

    if [ -z "$SIZE" ]; then
        echo "SIZE environment variable is not set. Please set it to one of the following: cpu, gpu-8g, gpu-16g, apple"
        exit 1
    fi
}

detect_gpu
detect_gpu_size

SIZE="${SIZE:-$GPU_SIZE}" # default to cpu
export MODELS="${MODELS:-/aio/${SIZE}/embeddings.yaml,/aio/${SIZE}/text-to-speech.yaml,/aio/${SIZE}/image-gen.yaml,/aio/${SIZE}/text-to-text.yaml,/aio/${SIZE}/speech-to-text.yaml,/aio/${SIZE}/vision.yaml}"

check_vars

echo "Starting LocalAI with the following models: $MODELS"

/build/entrypoint.sh "$@"