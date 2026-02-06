+++
title = "Sherpa-ONNX Backend Implementation Plan"
description = "Technical plan for integrating Sherpa-ONNX TTS/ASR backend into LocalAI"
draft = true
+++

# Sherpa-ONNX Backend Implementation Plan

## Overview

This document outlines the plan to integrate [Sherpa-ONNX](https://k2-fsa.github.io/sherpa/onnx/) as a new backend for LocalAI, focusing initially on Text-to-Speech (TTS) capabilities with GPU acceleration support, and later expanding to Automatic Speech Recognition (ASR), Voice Activity Detection (VAD), and other audio processing features.

Sherpa-ONNX is a comprehensive speech processing toolkit that provides:
- **TTS**: VITS, Matcha, Kokoro, Piper, KittenTTS, PocketTTS models
- **ASR**: Streaming and non-streaming recognition (Whisper, Paraformer, Zipformer, etc.)
- **VAD**: Voice Activity Detection
- **Keyword Spotting**: Wake word detection
- **Speaker Diarization**: Identify different speakers
- **Audio Tagging**: Content classification
- **Spoken Language Identification**: Language detection

## Table of Contents

1. [Component Requirements](#component-requirements)
2. [Dependencies Analysis](#dependencies-analysis)
3. [GPU Acceleration Requirements](#gpu-acceleration-requirements)
4. [Build Strategy](#build-strategy)
5. [Implementation Phases](#implementation-phases)
6. [Architecture Design](#architecture-design)
7. [Testing Strategy](#testing-strategy)
8. [User Documentation](#user-documentation)

---

## Component Requirements

### Core Components

#### 1. ONNX Runtime
- **Repository**: https://github.com/microsoft/onnxruntime
- **Version**: v1.24.1
- **Commit**: `470ae16099a74fe05e31f2530489332c0525edb5`
- **Purpose**: Neural network inference engine
- **Build Strategy**: Build from source with specific commit to ensure reproducibility
- **GPU Providers**:
  - **CUDA**: NVIDIA GPUs (CUDA 11.8+ with cuDNN 8.x, or CUDA 12.x with cuDNN 9.x)
  - **MIGraphX**: AMD ROCm GPUs (ROCm 5.4-7.2+) - Note: Old ROCm EP deprecated in 1.23
  - **DirectML**: Windows DirectX 12 GPUs

#### 2. Sherpa-ONNX C++ Library
- **Repository**: https://github.com/k2-fsa/sherpa-onnx
- **Version**: v1.12.23
- **Commit**: `7e227a529be6c383134a358c5744d0eb1cb5ae1f`
- **Purpose**: Core speech processing functionality
- **Build System**: CMake-based
- **Language**: C++17
- **Build Strategy**: Build from source, use pre-installed ONNX Runtime

#### 3. Sherpa-ONNX Go Bindings
- **Repository**: https://github.com/k2-fsa/sherpa-onnx-go
- **Purpose**: Go API wrapping C++ library via CGO
- **Note**: We will **NOT** use the prebuilt Go bindings. Instead, we'll create our own CGO wrappers to link against our custom-built Sherpa-ONNX with GPU support.

---

## Dependencies Analysis

### Dependency Management Strategy

Following LocalAI's pattern (like llama.cpp), we will:
1. **Build ONNX Runtime ourselves** from a specific commit
2. **Build Sherpa-ONNX ourselves** from a specific commit, using our ONNX Runtime
3. **Control all nested dependencies** by either:
   - Pre-downloading specific versions to avoid FetchContent's automatic fetching
   - Using Sherpa-ONNX's built-in FetchContent (which uses specific tagged releases)

### Native C++ Dependencies

Sherpa-ONNX uses CMake FetchContent to download these dependencies at **specific tagged versions**:

#### Fetched by Sherpa-ONNX (Specific Tags)
1. **kaldi-native-fbank** v1.22.3
   - Purpose: Audio feature extraction (MFCC, filterbank) for ASR
   - URL: `https://github.com/csukuangfj/kaldi-native-fbank/archive/refs/tags/v1.22.3.tar.gz`
   - Dependencies: kissfft-float (built internally)

2. **kaldi-decoder** v0.2.11
   - Purpose: FST-based decoding for ASR
   - URL: `https://github.com/k2-fsa/kaldi-decoder/archive/refs/tags/v0.2.11.tar.gz`
   - Dependencies: OpenFST (via sherpa-onnx-fst wrappers)

3. **simple-sentencepiece** v0.7
   - Purpose: Subword tokenization
   - URL: `https://github.com/pkufool/simple-sentencepiece/archive/refs/tags/v0.7.tar.gz`

4. **pybind11** v3.0.0 (if Python enabled - we won't enable)
   - URL: `https://github.com/pybind/pybind11/archive/refs/tags/v3.0.0.tar.gz`

5. **googletest** v1.13.0 (if tests enabled - we won't enable)
   - URL: `https://github.com/google/googletest/archive/refs/tags/v1.13.0.tar.gz`

#### TTS-Specific Dependencies (SHERPA_ONNX_ENABLE_TTS=ON)
6. **piper-phonemize**
   - Purpose: Grapheme-to-phoneme conversion
   - Fetched via CMake

7. **espeak-ng-for-piper**
   - Purpose: Phonemization backend
   - Fetched via CMake

8. **ucd**
   - Purpose: Unicode character database
   - Fetched via CMake

#### Other Dependencies (Header-only or minimal)
9. **nlohmann/json** - JSON parsing (header-only)
10. **websocketpp** - WebSocket support (optional, for server features we won't use)
11. **asio** - Async I/O (optional)
12. **portaudio** - Audio I/O (optional, not needed for our backend)
13. **hclust-cpp** - Hierarchical clustering for diarization (if enabled)

### Build CMake Configuration

```bash
cmake -DCMAKE_BUILD_TYPE=Release \
      -DSHERPA_ONNX_ENABLE_GPU=ON \
      -DSHERPA_ONNX_ENABLE_TTS=ON \
      -DSHERPA_ONNX_ENABLE_BINARY=OFF \
      -DSHERPA_ONNX_ENABLE_PYTHON=OFF \
      -DSHERPA_ONNX_ENABLE_TESTS=OFF \
      -DSHERPA_ONNX_ENABLE_C_API=ON \
      -DBUILD_SHARED_LIBS=ON \
      -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
      -DONNXRUNTIME_DIR=/path/to/our/onnxruntime/install \
      ..
```

**Key Point**: Sherpa-ONNX's FetchContent already uses **specific tagged releases**, not arbitrary commits. These are pinned versions, so the builds are reproducible. We don't need to pre-download these unless we want to override versions.

---

## GPU Acceleration Requirements

### Overview
GPU acceleration in Sherpa-ONNX is provided through ONNX Runtime execution providers. We must build ONNX Runtime from source with GPU support.

### NVIDIA CUDA Support

#### Requirements
- **CUDA Toolkit**: 11.8+ or 12.x (12.x recommended)
- **cuDNN**: 8.x (for CUDA 11.8) or 9.x (for CUDA 12.x)
- **Compute Capability**: 6.0+ (Pascal architecture or newer)
- **Additional**: Zlib (required for cuDNN 8/9 on Linux)

#### Build Process
1. **Build ONNX Runtime with CUDA** from commit `470ae16099a74fe05e31f2530489332c0525edb5`:
   ```bash
   git clone https://github.com/microsoft/onnxruntime.git
   cd onnxruntime
   git checkout 470ae16099a74fe05e31f2530489332c0525edb5
   git submodule update --init --recursive
   ./build.sh --use_cuda --cuda_home /usr/local/cuda --cudnn_home /usr/local/cudnn \
              --config Release --parallel --skip_tests \
              --cmake_extra_defines CMAKE_CUDA_ARCHITECTURES="60;70;75;80;86;89;90"
   make install DESTDIR=/opt/onnxruntime
   ```

2. **Build Sherpa-ONNX with GPU Support** from commit `7e227a529be6c383134a358c5744d0eb1cb5ae1f`:
   ```bash
   git clone https://github.com/k2-fsa/sherpa-onnx.git
   cd sherpa-onnx
   git checkout 7e227a529be6c383134a358c5744d0eb1cb5ae1f
   mkdir build && cd build
   cmake -DCMAKE_BUILD_TYPE=Release \
         -DSHERPA_ONNX_ENABLE_GPU=ON \
         -DSHERPA_ONNX_ENABLE_TTS=ON \
         -DSHERPA_ONNX_ENABLE_BINARY=OFF \
         -DBUILD_SHARED_LIBS=ON \
         -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
         -DONNXRUNTIME_DIR=/opt/onnxruntime \
         ..
   make -j$(nproc)
   make install DESTDIR=/opt/sherpa-onnx
   ```

3. **Build Go Backend**:
   - Create custom CGO wrapper linking against our Sherpa-ONNX build
   - Static or dynamic linking based on LocalAI's preference

#### Runtime Configuration
- Set `provider="cuda"` in TTS/ASR config
- Control GPU selection via `CUDA_VISIBLE_DEVICES` environment variable

### AMD ROCm Support

#### Requirements
- **ROCm**: 5.4 - 7.2+ (latest recommended)
- **Provider**: MIGraphX (ROCm EP deprecated as of ONNX Runtime 1.23)
- **Platform**: Ubuntu-based Linux distributions
- **GPUs**: GCN architecture and newer

#### Build Process
1. **Install ROCm SDK**:
   ```bash
   # Follow AMD's ROCm installation guide
   wget https://repo.radeon.com/amdgpu-install/latest/ubuntu/jammy/amdgpu-install_*.deb
   sudo apt install ./amdgpu-install_*.deb
   sudo amdgpu-install --usecase=rocm
   ```

2. **Build ONNX Runtime with MIGraphX**:
   ```bash
   git clone https://github.com/microsoft/onnxruntime.git
   cd onnxruntime
   git checkout 470ae16099a74fe05e31f2530489332c0525edb5
   git submodule update --init --recursive
   ./build.sh --use_migraphx --migraphx_home /opt/rocm \
              --config Release --parallel --skip_tests
   make install DESTDIR=/opt/onnxruntime
   ```

3. **Build Sherpa-ONNX**: Same as CUDA but using MIGraphX-enabled ONNX Runtime

#### Runtime Configuration
- Set `provider="migraphx"` in config
- Ensure `LD_LIBRARY_PATH` includes ROCm libraries

### DirectML (Windows GPU)

#### Requirements
- **OS**: Windows 10/11
- **API**: DirectX 12
- **GPUs**: NVIDIA Kepler+, AMD GCN1+, Intel Haswell+

#### Build Process
- Build ONNX Runtime with `--use_dml`
- Lower priority for initial implementation

---

## Build Strategy

### Multi-Stage Docker Build

Following LocalAI's pattern (like llama.cpp), we'll use multi-stage builds with pinned commits:

#### Stage 1: ONNX Runtime Builder (Per GPU Type)

**CPU Builder**:
```dockerfile
FROM ubuntu:24.04 AS onnxruntime-builder-cpu
RUN apt-get update && apt-get install -y git cmake build-essential python3
RUN git clone https://github.com/microsoft/onnxruntime.git /onnxruntime && \
    cd /onnxruntime && \
    git checkout 470ae16099a74fe05e31f2530489332c0525edb5 && \
    git submodule update --init --recursive
RUN cd /onnxruntime && \
    ./build.sh --config Release --parallel --skip_tests && \
    make install DESTDIR=/opt/onnxruntime
```

**CUDA Builder**:
```dockerfile
FROM nvidia/cuda:12.4.0-devel-ubuntu24.04 AS onnxruntime-builder-cuda
RUN apt-get update && apt-get install -y git cmake build-essential python3 zlib1g-dev wget
# Install cuDNN 9
RUN wget https://developer.download.nvidia.com/compute/cudnn/9.0.0/local_installers/cudnn-local-repo-ubuntu2404-9.0.0_1.0-1_amd64.deb && \
    dpkg -i cudnn-local-repo-ubuntu2404-9.0.0_1.0-1_amd64.deb && \
    apt-get update && apt-get install -y cudnn
# Build ONNX Runtime
RUN git clone https://github.com/microsoft/onnxruntime.git /onnxruntime && \
    cd /onnxruntime && \
    git checkout 470ae16099a74fe05e31f2530489332c0525edb5 && \
    git submodule update --init --recursive
RUN cd /onnxruntime && \
    ./build.sh --use_cuda --cuda_home /usr/local/cuda --cudnn_home /usr \
               --config Release --parallel --skip_tests \
               --cmake_extra_defines CMAKE_CUDA_ARCHITECTURES="60;70;75;80;86;89;90" && \
    make install DESTDIR=/opt/onnxruntime
```

**ROCm Builder**:
```dockerfile
FROM rocm/dev-ubuntu-24.04:6.4.4 AS onnxruntime-builder-rocm
RUN apt-get update && apt-get install -y git cmake python3
RUN git clone https://github.com/microsoft/onnxruntime.git /onnxruntime && \
    cd /onnxruntime && \
    git checkout 470ae16099a74fe05e31f2530489332c0525edb5 && \
    git submodule update --init --recursive
RUN cd /onnxruntime && \
    ./build.sh --use_migraphx --migraphx_home /opt/rocm \
               --config Release --parallel --skip_tests && \
    make install DESTDIR=/opt/onnxruntime
```

#### Stage 2: Sherpa-ONNX Builder

```dockerfile
FROM onnxruntime-builder-${BUILD_TYPE} AS sherpa-builder
ARG BUILD_TYPE=cpu
RUN git clone https://github.com/k2-fsa/sherpa-onnx.git /sherpa-onnx && \
    cd /sherpa-onnx && \
    git checkout 7e227a529be6c383134a358c5744d0eb1cb5ae1f
WORKDIR /sherpa-onnx/build
RUN cmake -DCMAKE_BUILD_TYPE=Release \
          -DSHERPA_ONNX_ENABLE_GPU=$([[ "$BUILD_TYPE" != "cpu" ]] && echo "ON" || echo "OFF") \
          -DSHERPA_ONNX_ENABLE_TTS=ON \
          -DSHERPA_ONNX_ENABLE_BINARY=OFF \
          -DSHERPA_ONNX_ENABLE_PYTHON=OFF \
          -DSHERPA_ONNX_ENABLE_TESTS=OFF \
          -DSHERPA_ONNX_ENABLE_C_API=ON \
          -DBUILD_SHARED_LIBS=ON \
          -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
          -DONNXRUNTIME_DIR=/opt/onnxruntime \
          .. && \
    make -j$(nproc) && \
    make install DESTDIR=/opt/sherpa-onnx
```

#### Stage 3: Go Backend Builder

```dockerfile
FROM sherpa-builder AS backend-builder
RUN apt-get install -y golang-1.21
COPY backend/go/sherpa-onnx /build
WORKDIR /build
ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/opt/sherpa-onnx/usr/local/include"
ENV CGO_LDFLAGS="-L/opt/sherpa-onnx/usr/local/lib -L/opt/onnxruntime/usr/local/lib -lsherpa-onnx -lonnxruntime"
RUN go build -o sherpa-onnx-backend .
```

#### Stage 4: Runtime Image

```dockerfile
FROM ubuntu:24.04 AS runtime
ARG BUILD_TYPE=cpu
COPY --from=backend-builder /build/sherpa-onnx-backend /usr/local/bin/
COPY --from=backend-builder /opt/sherpa-onnx/usr/local/lib/* /usr/local/lib/
COPY --from=backend-builder /opt/onnxruntime/usr/local/lib/* /usr/local/lib/
# Copy CUDA/ROCm runtime libraries if GPU build
RUN ldconfig
ENTRYPOINT ["/usr/local/bin/sherpa-onnx-backend"]
```

### Build Variants

1. **CPU** (`sherpa-onnx-cpu`): Default CPU-only build
2. **CUDA 12** (`sherpa-onnx-cuda12`): NVIDIA GPU support with CUDA 12.x + cuDNN 9
3. **CUDA 11** (`sherpa-onnx-cuda11`): NVIDIA GPU support with CUDA 11.8 + cuDNN 8 (optional)
4. **ROCm** (`sherpa-onnx-hipblas`): AMD GPU support with ROCm 6.4+

### Makefile Integration

```makefile
# Backend definition (using . context like whisper backend)
BACKEND_SHERPA_ONNX = sherpa-onnx|golang|.|false|true

# Generate docker build target
$(eval $(call generate-docker-build-target,$(BACKEND_SHERPA_ONNX)))

# Add to .NOTPARALLEL
.NOTPARALLEL: ... backends/sherpa-onnx

# Add to prepare-test-extra
prepare-test-extra: protogen-go
	$(MAKE) -C backend/go/sherpa-onnx

# Add to test-extra
test-extra: prepare-test-extra
	$(MAKE) -C backend/go/sherpa-onnx test

# Add to backends target
docker-build-backends: ... docker-build-sherpa-onnx
```

### GitHub Actions Workflow

Add to `.github/workflows/backend.yml`:

```yaml
include:
  # CPU build
  - build-type: ''
    cuda-major-version: ''
    cuda-minor-version: ''
    platforms: 'linux/amd64,linux/arm64'
    tag-suffix: '-cpu-sherpa-onnx'
    makeflags: '--jobs=4 --output-sync=target'
    runs-on: 'ubuntu-24.04'

  # CUDA 12 build
  - build-type: 'cublas'
    cuda-major-version: '12'
    cuda-minor-version: '6'
    platforms: 'linux/amd64'
    tag-suffix: '-gpu-nvidia-cuda-12-sherpa-onnx'
    makeflags: '--jobs=4 --output-sync=target'
    runs-on: 'ubuntu-24.04'

  # ROCm build
  - build-type: 'hipblas'
    platforms: 'linux/amd64'
    tag-suffix: '-gpu-amd-hipblas-sherpa-onnx'
    base-image: "rocm/dev-ubuntu-24.04:6.4.4"
    makeflags: '--jobs=4 --output-sync=target'
    runs-on: 'ubuntu-24.04'
```

### Backend Index

Add to `backend/index.yaml`:

```yaml
## metas section
sherpa-onnx-meta: &sherpa-onnx
  author: LocalAI
  tags:
    - tts
    - asr
    - audio
  description: |
    Sherpa-ONNX backend for text-to-speech, speech recognition, and audio processing
  urls:
    - https://k2-fsa.github.io/sherpa/onnx/

# At end of file with other images
- <<: *sherpa-onnx
  name: sherpa-onnx-cpu
  tag: "latest"
  platforms:
    - linux/amd64
    - linux/arm64

- <<: *sherpa-onnx
  name: sherpa-onnx-cpu
  tag: "master"
  platforms:
    - linux/amd64
    - linux/arm64

- <<: *sherpa-onnx
  name: sherpa-onnx-cuda12
  tag: "latest"
  platforms:
    - linux/amd64

- <<: *sherpa-onnx
  name: sherpa-onnx-cuda12
  tag: "master"
  platforms:
    - linux/amd64

- <<: *sherpa-onnx
  name: sherpa-onnx-hipblas
  tag: "latest"
  platforms:
    - linux/amd64

- <<: *sherpa-onnx
  name: sherpa-onnx-hipblas
  tag: "master"
  platforms:
    - linux/amd64
```

---

## Implementation Phases

### Phase 1: TTS Support (CPU-only)

**Goal**: Get basic TTS working with CPU inference, establish build system.

#### Week 1: Backend Structure & Build System
- Create `backend/go/sherpa-onnx/` directory structure
- Implement minimal Go backend (main.go, backend.go)
- Create Dockerfile for CPU-only build
- Build ONNX Runtime (CPU) from commit `470ae16099a74fe05e31f2530489332c0525edb5`
- Build Sherpa-ONNX from commit `7e227a529be6c383134a358c5744d0eb1cb5ae1f`
- Create CGO wrapper for TTS functionality
- Verify end-to-end build

#### Week 2: TTS Implementation
- Implement `Load(*pb.ModelOptions)` for VITS model loading
- Implement `TTS(*pb.TTSRequest)` for text-to-speech generation
- Support basic parameters (text, speaker ID, speed)
- Generate WAV audio files
- Test with small VITS model

#### Week 3: Integration & Basic Testing
- Integrate with LocalAI core via gRPC
- Create basic unit tests (model loading, TTS generation)
- Test with very small TTS model for CI (CPU-only)
- Add Makefile targets
- Update backend/index.yaml

**Deliverables**:
- ✅ Working TTS backend (CPU-only)
- ✅ Dockerfile builds successfully
- ✅ Basic functionality test passes with small model
- ✅ Integrated into LocalAI build system

### Phase 2: GPU Acceleration (CUDA)

**Goal**: Add NVIDIA GPU support, validate GPU inference works.

#### Week 4-5: CUDA Build System
- Create CUDA builder Dockerfile
- Build ONNX Runtime with CUDA provider
- Build Sherpa-ONNX with GPU support
- Update CGO wrapper for GPU libraries
- Add provider configuration to backend
- Create CUDA build variant in workflows

#### Week 6: GPU Testing & Validation
- Test GPU inference (manual testing on GPU hardware)
- Verify provider selection (CPU vs CUDA)
- Basic performance check (GPU faster than CPU)
- Document GPU setup requirements

**Deliverables**:
- ✅ TTS backend with CUDA support
- ✅ Build system for CUDA variant
- ✅ Manual validation on GPU hardware
- ✅ GPU provider configuration working

### Phase 3: AMD ROCm Support

**Goal**: Add AMD GPU support via MIGraphX.

#### Week 7-8: ROCm Build System
- Create ROCm builder Dockerfile
- Build ONNX Runtime with MIGraphX
- Test on AMD hardware (if available)
- Add ROCm build variant

**Deliverables**:
- ✅ TTS backend with ROCm support
- ✅ Build system for ROCm variant
- ✅ Best-effort validation (if hardware available)

### Phase 4: ASR Support

**Goal**: Add speech-to-text functionality.

#### Week 9-10: ASR Implementation
- Implement `AudioTranscription(*pb.TranscriptRequest)` method
- Add support for Whisper models (or other ASR models)
- Handle audio format conversion
- Test with small ASR model

#### Week 11: ASR Testing
- Basic functionality test with small model
- Integration with LocalAI transcription API

**Deliverables**:
- ✅ ASR backend implementation
- ✅ Basic functionality test
- ✅ Documentation

### Phase 5: Additional Features (Future)

**Goal**: Implement VAD, keyword spotting, speaker diarization.

- Voice Activity Detection
- Keyword spotting
- Speaker diarization
- Additional model types (Matcha, Kokoro, etc.)

---

## Architecture Design

### Backend Structure

```
backend/go/sherpa-onnx/
├── main.go                 # gRPC server entry point
├── backend.go             # Backend struct (implements AIModel)
├── tts.go                 # TTS-specific logic
├── asr.go                 # ASR-specific logic (Phase 4)
├── model_loader.go        # Model loading and configuration
├── audio_utils.go         # Audio format conversion
├── Makefile              # Build configuration
├── package.sh            # Packaging script
├── go.mod                # Go module
└── README.md             # Backend documentation
```

### Backend Implementation

```go
package main

import (
    "github.com/mudler/LocalAI/pkg/grpc/base"
    pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// #cgo CFLAGS: -I/usr/local/include/sherpa-onnx
// #cgo LDFLAGS: -L/usr/local/lib -lsherpa-onnx -lonnxruntime
// #include <sherpa-onnx/c-api/c-api.h>
// #include <stdlib.h>
import "C"

type SherpaBackend struct {
    base.SingleThread

    // TTS
    tts       unsafe.Pointer  // C.SherpaOnnxOfflineTts*
    
    // ASR (Phase 4)
    asr       unsafe.Pointer  // C.SherpaOnnxOfflineRecognizer*
    
    // Configuration
    modelPath string
    provider  string  // "cpu", "cuda", "migraphx"
}

func (s *SherpaBackend) Load(opts *pb.ModelOptions) error {
    // Detect model type and load appropriate model
    modelType := detectModelType(opts)
    
    switch modelType {
    case "tts":
        return s.loadTTSModel(opts)
    case "asr":
        return s.loadASRModel(opts)
    default:
        return fmt.Errorf("unknown model type: %s", modelType)
    }
}

func (s *SherpaBackend) TTS(req *pb.TTSRequest) (*pb.Result, error) {
    // Generate audio using C API
    cText := C.CString(req.Text)
    defer C.free(unsafe.Pointer(cText))
    
    // Call C API
    audio := C.SherpaOnnxOfflineTtsGenerate(s.tts, cText, C.int(sid), C.float(speed))
    defer C.SherpaOnnxDestroyOfflineTtsGeneratedAudio(audio)
    
    // Save to file
    cDst := C.CString(req.Dst)
    defer C.free(unsafe.Pointer(cDst))
    
    success := C.SherpaOnnxOfflineTtsGeneratedAudioSave(audio, cDst)
    if success == 0 {
        return &pb.Result{Success: false, Message: "failed to save audio"}, nil
    }
    
    return &pb.Result{Success: true, Message: "audio generated"}, nil
}
```

### Configuration Schema

```yaml
name: sherpa-vits-en
backend: sherpa-onnx
type: tts

parameters:
  model_type: vits  # vits, matcha, kokoro
  model: model.onnx
  tokens: tokens.txt
  lexicon: lexicon.txt
  
  # GPU config
  provider: cuda  # cpu, cuda, migraphx
  gpu_device: 0
  
  # TTS params
  num_speakers: 1
  default_speaker: 0
  default_speed: 1.0
  
  # Runtime
  num_threads: 4
```

---

## Testing Strategy

### Unit Tests (backend/go/sherpa-onnx/)

1. **Model Loading Test**
   - Test loading VITS configuration
   - Test error handling for invalid paths

2. **TTS Functionality Test**
   - Use a **very small VITS model** (< 50MB for CI)
   - Generate audio from simple text ("Hello world")
   - Validate WAV file is created and has content
   - CPU-only (no GPU testing in CI)

### Integration Tests

1. **LocalAI Integration**
   - Test gRPC communication
   - Test TTS endpoint via LocalAI API
   - CPU-only with small model

### Manual Testing (GPU)

- Manual validation on GPU hardware when available
- Test CUDA provider selection
- Basic performance check (GPU should be faster)
- No automated GPU testing initially

### Test Model

We need to identify or create a **tiny VITS model** (< 50MB) for CI testing:
- Minimal vocabulary
- Single speaker
- Low quality acceptable
- Only for functional testing, not quality

---

## User Documentation

Users will install the backend and models from the LocalAI gallery. Documentation should focus on:

### 1. Feature Overview (`docs/content/features/sherpa-onnx.md`)

```markdown
## Sherpa-ONNX Backend

Sherpa-ONNX provides text-to-speech (TTS) and automatic speech recognition (ASR) capabilities.

### Features
- Text-to-Speech with multiple model architectures (VITS, Matcha, Kokoro)
- Multi-speaker support
- GPU acceleration (NVIDIA CUDA, AMD ROCm)

### Installation

The Sherpa-ONNX backend is available in the LocalAI gallery.

**CPU-only**:
```yaml
backend: sherpa-onnx-cpu
```

**NVIDIA GPU (CUDA 12)**:
```yaml
backend: sherpa-onnx-cuda12
```

**AMD GPU (ROCm)**:
```yaml
backend: sherpa-onnx-hipblas
```

### Supported Models

Download TTS models from the gallery or from [Sherpa-ONNX pretrained models](https://k2-fsa.github.io/sherpa/onnx/pretrained_models/index.html).
```

### 2. Configuration Guide

```markdown
## Configuration

Create a model configuration file:

```yaml
name: my-tts-voice
backend: sherpa-onnx
type: tts

parameters:
  model_type: vits
  model: model.onnx
  tokens: tokens.txt
  lexicon: lexicon.txt  # optional
  
  # Multi-speaker models
  num_speakers: 8
  default_speaker: 0
  
  # Speed control (0.5-2.0)
  default_speed: 1.0
  
  # GPU acceleration
  provider: cuda  # or cpu, migraphx
  gpu_device: 0
  
  # CPU threads (if provider=cpu)
  num_threads: 4
```

### Model Types

**VITS**: General purpose TTS, multi-speaker support
**Matcha**: High quality, slower inference
**Kokoro**: Multi-lingual (Chinese/English)

### API Usage

```bash
curl http://localhost:8080/tts \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-tts-voice",
    "input": "Hello, this is a test.",
    "voice": "0"
  }' \
  --output speech.wav
```
```

### 3. Troubleshooting

```markdown
## Troubleshooting

### GPU not detected
- Verify CUDA/ROCm installation
- Check `CUDA_VISIBLE_DEVICES` environment variable
- Ensure correct backend variant (cuda12/hipblas)

### Audio quality issues
- Adjust speed parameter (0.8-1.2)
- Try different speaker IDs
- Use higher quality model

### Out of memory
- Reduce num_threads
- Use CPU provider
- Use smaller model
```

That's the complete documentation needed - users just need to know how to configure and use the backend.

---

## Summary

### Key Points

1. **Pinned Dependencies**:
   - ONNX Runtime: v1.24.1 (commit `470ae16099a74fe05e31f2530489332c0525edb5`)
   - Sherpa-ONNX: v1.12.23 (commit `7e227a529be6c383134a358c5744d0eb1cb5ae1f`)
   - Nested dependencies: Sherpa-ONNX's CMake uses specific tagged releases (reproducible)

2. **Build Strategy**:
   - Build ONNX Runtime ourselves from specific commit
   - Build Sherpa-ONNX ourselves, providing pre-built ONNX Runtime
   - Sherpa-ONNX will FetchContent its dependencies at specific versions
   - Create custom CGO wrapper (not using sherpa-onnx-go prebuilts)

3. **GPU Support**:
   - CUDA (NVIDIA) - Primary target
   - MIGraphX (AMD ROCm) - Secondary target
   - Only ONNX Runtime needs GPU support, all other deps are CPU-only

4. **Testing**:
   - Basic functionality test with very small model (CPU-only)
   - No performance tests or GPU tests in CI
   - Manual GPU validation when hardware available

5. **Documentation**:
   - User-focused: How to configure models and use the backend
   - No developer documentation beyond this plan
   - Gallery integration for easy installation

### Next Steps

1. Review and approve this plan
2. Identify or create tiny VITS model for CI testing (< 50MB)
3. Begin Phase 1: CPU-only TTS implementation
4. Iterate based on feedback

---

## References

- **Sherpa-ONNX Documentation**: https://k2-fsa.github.io/sherpa/onnx/
- **Sherpa-ONNX GitHub**: https://github.com/k2-fsa/sherpa-onnx
- **ONNX Runtime Documentation**: https://onnxruntime.ai/docs/
- **ONNX Runtime Build Guide**: https://onnxruntime.ai/docs/build/
- **LocalAI Backend Documentation**: /home/rich/go/src/github.com/mudler/LocalAI/AGENTS.md
