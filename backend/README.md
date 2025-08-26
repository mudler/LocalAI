# LocalAI Backend Architecture

This directory contains the core backend infrastructure for LocalAI, including the gRPC protocol definition, multi-language Dockerfiles, and language-specific backend implementations.

## Overview

LocalAI uses a unified gRPC-based architecture that allows different programming languages to implement AI backends while maintaining consistent interfaces and capabilities. The backend system supports multiple hardware acceleration targets and provides a standardized way to integrate various AI models and frameworks.

## Architecture Components

### 1. Protocol Definition (`backend.proto`)

The `backend.proto` file defines the gRPC service interface that all backends must implement. This ensures consistency across different language implementations and provides a contract for communication between LocalAI core and backend services.

#### Core Services

- **Text Generation**: `Predict`, `PredictStream` for LLM inference
- **Embeddings**: `Embedding` for text vectorization
- **Image Generation**: `GenerateImage` for stable diffusion and image models
- **Audio Processing**: `AudioTranscription`, `TTS`, `SoundGeneration`
- **Video Generation**: `GenerateVideo` for video synthesis
- **Object Detection**: `Detect` for computer vision tasks
- **Vector Storage**: `StoresSet`, `StoresGet`, `StoresFind` for RAG operations
- **Reranking**: `Rerank` for document relevance scoring
- **Voice Activity Detection**: `VAD` for audio segmentation

#### Key Message Types

- **`PredictOptions`**: Comprehensive configuration for text generation
- **`ModelOptions`**: Model loading and configuration parameters
- **`Result`**: Standardized response format
- **`StatusResponse`**: Backend health and memory usage information

### 2. Multi-Language Dockerfiles

The backend system provides language-specific Dockerfiles that handle the build environment and dependencies for different programming languages:

- `Dockerfile.python`
- `Dockerfile.golang`
- `Dockerfile.llama-cpp`

### 3. Language-Specific Implementations

#### Python Backends (`python/`)
- **transformers**: Hugging Face Transformers framework
- **vllm**: High-performance LLM inference
- **mlx**: Apple Silicon optimization
- **diffusers**: Stable Diffusion models
- **Audio**: bark, coqui, faster-whisper, kitten-tts
- **Vision**: mlx-vlm, rfdetr
- **Specialized**: rerankers, chatterbox, kokoro

#### Go Backends (`go/`)
- **whisper**: OpenAI Whisper speech recognition in Go with GGML cpp backend (whisper.cpp)
- **stablediffusion-ggml**: Stable Diffusion in Go with GGML Cpp backend
- **huggingface**: Hugging Face model integration
- **piper**: Text-to-speech synthesis Golang with C bindings using rhaspy/piper
- **bark-cpp**: Bark TTS models Golang with Cpp bindings
- **local-store**: Vector storage backend

#### C++ Backends (`cpp/`)
- **llama-cpp**: Llama.cpp integration
- **grpc**: GRPC utilities and helpers

## Hardware Acceleration Support

### CUDA (NVIDIA)
- **Versions**: CUDA 11.x, 12.x
- **Features**: cuBLAS, cuDNN, TensorRT optimization
- **Targets**: x86_64, ARM64 (Jetson)

### ROCm (AMD)
- **Features**: HIP, rocBLAS, MIOpen
- **Targets**: AMD GPUs with ROCm support

### Intel
- **Features**: oneAPI, Intel Extension for PyTorch
- **Targets**: Intel GPUs, XPUs, CPUs

### Vulkan
- **Features**: Cross-platform GPU acceleration
- **Targets**: Windows, Linux, Android, macOS

### Apple Silicon
- **Features**: MLX framework, Metal Performance Shaders
- **Targets**: M1/M2/M3 Macs

## Backend Registry (`index.yaml`)

The `index.yaml` file serves as a central registry for all available backends, providing:

- **Metadata**: Name, description, license, icons
- **Capabilities**: Hardware targets and optimization profiles
- **Tags**: Categorization for discovery
- **URLs**: Source code and documentation links

## Building Backends

### Prerequisites
- Docker with multi-architecture support
- Appropriate hardware drivers (CUDA, ROCm, etc.)
- Build tools (make, cmake, compilers)

### Build Commands

Example of build commands with Docker

```bash
# Build Python backend
docker build -f backend/Dockerfile.python \
  --build-arg BACKEND=transformers \
  --build-arg BUILD_TYPE=cublas12 \
  --build-arg CUDA_MAJOR_VERSION=12 \
  --build-arg CUDA_MINOR_VERSION=0 \
  -t localai-backend-transformers .

# Build Go backend
docker build -f backend/Dockerfile.golang \
  --build-arg BACKEND=whisper \
  --build-arg BUILD_TYPE=cpu \
  -t localai-backend-whisper .

# Build C++ backend
docker build -f backend/Dockerfile.llama-cpp \
  --build-arg BACKEND=llama-cpp \
  --build-arg BUILD_TYPE=cublas12 \
  -t localai-backend-llama-cpp .
```

For ARM64/Mac builds, docker can't be used, and the makefile in the respective backend has to be used.

### Build Types

- **`cpu`**: CPU-only optimization
- **`cublas11`**: CUDA 11.x with cuBLAS
- **`cublas12`**: CUDA 12.x with cuBLAS
- **`hipblas`**: ROCm with rocBLAS
- **`intel`**: Intel oneAPI optimization
- **`vulkan`**: Vulkan-based acceleration
- **`metal`**: Apple Metal optimization

## Backend Development

### Creating a New Backend

1. **Choose Language**: Select Python, Go, or C++ based on requirements
2. **Implement Interface**: Implement the gRPC service defined in `backend.proto`
3. **Add Dependencies**: Create appropriate requirements files
4. **Configure Build**: Set up Dockerfile and build scripts
5. **Register Backend**: Add entry to `index.yaml`
6. **Test Integration**: Verify gRPC communication and functionality

### Backend Structure

```
backend-name/
├── backend.py/go/cpp    # Main implementation
├── requirements.txt      # Dependencies
├── Dockerfile           # Build configuration
├── install.sh           # Installation script
├── run.sh              # Execution script
├── test.sh             # Test script
└── README.md           # Backend documentation
```

### Required gRPC Methods

At minimum, backends must implement:
- `Health()` - Service health check
- `LoadModel()` - Model loading and initialization
- `Predict()` - Main inference endpoint
- `Status()` - Backend status and metrics

## Integration with LocalAI Core

Backends communicate with LocalAI core through gRPC:

1. **Service Discovery**: Core discovers available backends
2. **Model Loading**: Core requests model loading via `LoadModel`
3. **Inference**: Core sends requests via `Predict` or specialized endpoints
4. **Streaming**: Core handles streaming responses for real-time generation
5. **Monitoring**: Core tracks backend health and performance

## Performance Optimization

### Memory Management
- **Model Caching**: Efficient model loading and caching
- **Batch Processing**: Optimize for multiple concurrent requests
- **Memory Pinning**: GPU memory optimization for CUDA/ROCm

### Hardware Utilization
- **Multi-GPU**: Support for tensor parallelism
- **Mixed Precision**: FP16/BF16 for memory efficiency
- **Kernel Fusion**: Optimized CUDA/ROCm kernels

## Troubleshooting

### Common Issues

1. **GRPC Connection**: Verify backend service is running and accessible
2. **Model Loading**: Check model paths and dependencies
3. **Hardware Detection**: Ensure appropriate drivers and libraries
4. **Memory Issues**: Monitor GPU memory usage and model sizes

## Contributing

When contributing to the backend system:

1. **Follow Protocol**: Implement the exact gRPC interface
2. **Add Tests**: Include comprehensive test coverage
3. **Document**: Provide clear usage examples
4. **Optimize**: Consider performance and resource usage
5. **Validate**: Test across different hardware targets