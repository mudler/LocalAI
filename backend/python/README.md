# Python Backends for LocalAI

This directory contains Python-based AI backends for LocalAI, providing support for various AI models and hardware acceleration targets.

## Overview

The Python backends use a unified build system based on `libbackend.sh` that provides:
- **Automatic virtual environment management** with support for both `uv` and `pip`
- **Hardware-specific dependency installation** (CPU, CUDA, Intel, MLX, etc.)
- **Portable Python support** for standalone deployments
- **Consistent backend execution** across different environments

## Available Backends

### Core AI Models
- **transformers** - Hugging Face Transformers framework (PyTorch-based)
- **vllm** - High-performance LLM inference engine
- **mlx** - Apple Silicon optimized ML framework
- **exllama2** - ExLlama2 quantized models

### Audio & Speech
- **bark** - Text-to-speech synthesis
- **coqui** - Coqui TTS models
- **faster-whisper** - Fast Whisper speech recognition
- **kitten-tts** - Lightweight TTS
- **mlx-audio** - Apple Silicon audio processing
- **chatterbox** - TTS model
- **kokoro** - TTS models

### Computer Vision
- **diffusers** - Stable Diffusion and image generation
- **mlx-vlm** - Vision-language models for Apple Silicon
- **rfdetr** - Object detection models

### Specialized

- **rerankers** - Text reranking models

## Quick Start

### Prerequisites
- Python 3.10+ (default: 3.10.18)
- `uv` package manager (recommended) or `pip`
- Appropriate hardware drivers for your target (CUDA, Intel, etc.)

### Installation

Each backend can be installed individually:

```bash
# Navigate to a specific backend
cd backend/python/transformers

# Install dependencies
make transformers
# or
bash install.sh

# Run the backend
make run
# or
bash run.sh
```

### Using the Unified Build System

The `libbackend.sh` script provides consistent commands across all backends:

```bash
# Source the library in your backend script
source $(dirname $0)/../common/libbackend.sh

# Install requirements (automatically handles hardware detection)
installRequirements

# Start the backend server
startBackend $@

# Run tests
runUnittests
```

## Hardware Targets

The build system automatically detects and configures for different hardware:

- **CPU** - Standard CPU-only builds
- **CUDA** - NVIDIA GPU acceleration (supports CUDA 11/12)
- **Intel** - Intel XPU/GPU optimization
- **MLX** - Apple Silicon (M1/M2/M3) optimization
- **HIP** - AMD GPU acceleration

### Target-Specific Requirements

Backends can specify hardware-specific dependencies:
- `requirements.txt` - Base requirements
- `requirements-cpu.txt` - CPU-specific packages
- `requirements-cublas11.txt` - CUDA 11 packages
- `requirements-cublas12.txt` - CUDA 12 packages
- `requirements-intel.txt` - Intel-optimized packages
- `requirements-mps.txt` - Apple Silicon packages

## Configuration Options

### Environment Variables

- `PYTHON_VERSION` - Python version (default: 3.10)
- `PYTHON_PATCH` - Python patch version (default: 18)
- `BUILD_TYPE` - Force specific build target
- `USE_PIP` - Use pip instead of uv (default: false)
- `PORTABLE_PYTHON` - Enable portable Python builds
- `LIMIT_TARGETS` - Restrict backend to specific targets

### Example: CUDA 12 Only Backend

```bash
# In your backend script
LIMIT_TARGETS="cublas12"
source $(dirname $0)/../common/libbackend.sh
```

### Example: Intel-Optimized Backend

```bash
# In your backend script
LIMIT_TARGETS="intel"
source $(dirname $0)/../common/libbackend.sh
```

## Development

### Adding a New Backend

1. Create a new directory in `backend/python/`
2. Copy the template structure from `common/template/`
3. Implement your `backend.py` with the required gRPC interface
4. Add appropriate requirements files for your target hardware
5. Use `libbackend.sh` for consistent build and execution

### Testing

```bash
# Run backend tests
make test
# or
bash test.sh
```

### Building

```bash
# Install dependencies
make <backend-name>

# Clean build artifacts
make clean
```

## Architecture

Each backend follows a consistent structure:
```
backend-name/
├── backend.py          # Main backend implementation
├── requirements.txt    # Base dependencies
├── requirements-*.txt  # Hardware-specific dependencies
├── install.sh         # Installation script
├── run.sh            # Execution script
├── test.sh           # Test script
├── Makefile          # Build targets
└── test.py           # Unit tests
```

## Troubleshooting

### Common Issues

1. **Missing dependencies**: Ensure all requirements files are properly configured
2. **Hardware detection**: Check that `BUILD_TYPE` matches your system
3. **Python version**: Verify Python 3.10+ is available
4. **Virtual environment**: Use `ensureVenv` to create/activate environments

## Contributing

When adding new backends or modifying existing ones:
1. Follow the established directory structure
2. Use `libbackend.sh` for consistent behavior
3. Include appropriate requirements files for all target hardware
4. Add comprehensive tests
5. Update this README if adding new backend types
