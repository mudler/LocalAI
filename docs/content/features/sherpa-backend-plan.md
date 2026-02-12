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
- **Version**: v1.24.1 (or version compatible with chosen Sherpa-ONNX release)
- **Purpose**: Neural network inference engine
- **Build Strategy**: Download prebuilt upstream release tarballs (following the `silero-vad` backend pattern). The Makefile selects the correct package (CPU, GPU-CUDA, or ROCm) based on `BUILD_TYPE`.
- **Justification**: Upstream publishes well-tested binaries for all target platforms. Avoids 30-60+ minute source builds. The `silero-vad` backend already uses this approach successfully.
- **GPU Providers**:
  - **CUDA**: NVIDIA GPUs (CUDA 11.8+ with cuDNN 8.x, or CUDA 12.x with cuDNN 9.x) — use `onnxruntime-linux-x64-gpu-*.tgz`
  - **MIGraphX**: AMD ROCm GPUs (ROCm 5.4-7.2+) - Note: Old ROCm EP deprecated in 1.23 — use `onnxruntime-linux-x64-rocm-*.tgz` if available
  - **DirectML**: Windows DirectX 12 GPUs (lower priority)

#### 2. Sherpa-ONNX C++ Library
- **Repository**: https://github.com/k2-fsa/sherpa-onnx
- **Version**: v1.12.23
- **Commit**: `7e227a529be6c383134a358c5744d0eb1cb5ae1f`
- **Purpose**: Core speech processing functionality
- **Build System**: CMake-based
- **Language**: C++17
- **Build Strategy**: `git clone` + `cmake` build inside `backend/go/sherpa-onnx/Makefile`, linked against the downloaded ONNX Runtime

#### 3. Sherpa-ONNX Go Bindings
- **Repository**: https://github.com/k2-fsa/sherpa-onnx-go
- **Purpose**: Go API wrapping C++ library via CGO
- **Note**: We will **NOT** use the prebuilt Go bindings. Instead, we'll create our own CGO wrappers to link against our custom-built Sherpa-ONNX with GPU support.

---

## Dependencies Analysis

### Dependency Management Strategy

We will:
1. **Download prebuilt ONNX Runtime** from upstream releases (following the `silero-vad` pattern)
2. **Build Sherpa-ONNX ourselves** via `git clone` + `cmake` from a specific commit/tag, using the downloaded ONNX Runtime
3. **Let Sherpa-ONNX manage its nested dependencies** via its built-in FetchContent (which uses specific tagged releases)

Both steps happen inside `backend/go/sherpa-onnx/Makefile`, executed by the existing `backend/Dockerfile.golang`.

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
GPU acceleration in Sherpa-ONNX is provided through ONNX Runtime execution providers. We download the appropriate prebuilt ONNX Runtime package (CPU, GPU-CUDA, or ROCm) based on `BUILD_TYPE`, then build Sherpa-ONNX against it with `SHERPA_ONNX_ENABLE_GPU=ON`.

### NVIDIA CUDA Support

#### Requirements
- **CUDA Toolkit**: 11.8+ or 12.x (12.x recommended)
- **cuDNN**: 8.x (for CUDA 11.8) or 9.x (for CUDA 12.x)
- **Compute Capability**: 6.0+ (Pascal architecture or newer)
- **Additional**: Zlib (required for cuDNN 8/9 on Linux)

#### Build Process
1. **Download prebuilt ONNX Runtime with CUDA** (handled by Makefile):
   ```bash
   # The Makefile downloads the GPU variant automatically when BUILD_TYPE=cublas
   curl -L https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz \
     -o sources/onnxruntime/onnxruntime.tgz
   # Extract headers + libs to sources/onnxruntime/
   ```

2. **Build Sherpa-ONNX with GPU Support** from pinned commit:
   ```bash
   git clone https://github.com/k2-fsa/sherpa-onnx.git sources/sherpa-onnx
   cd sources/sherpa-onnx && git checkout 7e227a529be6c383134a358c5744d0eb1cb5ae1f
   mkdir build && cd build
   cmake -DCMAKE_BUILD_TYPE=Release \
         -DSHERPA_ONNX_ENABLE_GPU=ON \
         -DSHERPA_ONNX_ENABLE_TTS=ON \
         -DSHERPA_ONNX_ENABLE_BINARY=OFF \
         -DBUILD_SHARED_LIBS=ON \
         -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
         -DONNXRUNTIME_DIR=$(pwd)/../../onnxruntime \
         ..
   make -j$(nproc)
   make install DESTDIR=$(pwd)/../../sherpa-install
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
1. **ROCm SDK**: Provided by the base image (`rocm/dev-ubuntu-24.04:6.4.4`) via `backend/Dockerfile.golang`.

2. **Download prebuilt ONNX Runtime with ROCm** (handled by Makefile):
   ```bash
   # If upstream publishes ROCm binaries for our version:
   curl -L https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-x64-rocm-${ONNX_VERSION}.tgz \
     -o sources/onnxruntime/onnxruntime.tgz
   # Note: If ROCm prebuilt is unavailable for the chosen version,
   # fall back to CPU-only ONNX Runtime on ROCm builds.
   ```

3. **Build Sherpa-ONNX**: Same as CUDA but using ROCm-enabled ONNX Runtime

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

### Makefile-Driven Build (using existing `backend/Dockerfile.golang`)

Instead of a custom multi-stage Dockerfile, we use the existing `backend/Dockerfile.golang` which runs `make -C /LocalAI/backend/go/${BACKEND} build`. All library acquisition and compilation happens in `backend/go/sherpa-onnx/Makefile`, following the pattern established by `backend/go/silero-vad/Makefile`.

The Dockerfile already provides: `git`, `cmake`, `make`, `g++` (build-essential), `curl`, Go toolchain, protoc, and GPU-specific libraries (CUDA, ROCm, Vulkan) based on `BUILD_TYPE`.

#### Makefile Workflow

The `backend/go/sherpa-onnx/Makefile` performs these steps in order:

1. **Detect architecture/OS** (x86_64 vs aarch64, linux vs darwin)
2. **Download prebuilt ONNX Runtime** from upstream GitHub releases
   - CPU: `onnxruntime-linux-x64-${ONNX_VERSION}.tgz`
   - CUDA: `onnxruntime-linux-x64-gpu-${ONNX_VERSION}.tgz`
   - ROCm: `onnxruntime-linux-x64-rocm-${ONNX_VERSION}.tgz` (if available)
   - Extract to `sources/onnxruntime/`
3. **Clone and build Sherpa-ONNX** from pinned commit
   - `git clone` to `sources/sherpa-onnx/`
   - `cmake` with `-DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON`
   - `make -j$(nproc)` + `make install`
4. **Copy runtime libraries** to `backend-assets/lib/`
5. **Build Go binary** with CGO flags pointing to local headers/libs
6. **Package** via `package.sh` (bundles binary + shared libs)

```makefile
CURRENT_DIR=$(abspath ./)
GOCMD=go

ONNX_VERSION?=1.24.1
SHERPA_COMMIT?=7e227a529be6c383134a358c5744d0eb1cb5ae1f
ONNX_ARCH?=x64
ONNX_OS?=linux

# Detect ARM
ifneq (,$(findstring aarch64,$(shell uname -m)))
	ONNX_ARCH=aarch64
endif

# Determine ONNX Runtime package variant based on BUILD_TYPE
ifeq ($(BUILD_TYPE),cublas)
	ONNX_VARIANT=gpu
	SHERPA_GPU=ON
else ifeq ($(BUILD_TYPE),hipblas)
	ONNX_VARIANT=rocm
	SHERPA_GPU=ON
else
	ONNX_VARIANT=
	SHERPA_GPU=OFF
endif

# Download ONNX Runtime (prebuilt, like silero-vad)
sources/onnxruntime:
	mkdir -p sources/onnxruntime
	curl -L https://github.com/microsoft/onnxruntime/releases/download/v$(ONNX_VERSION)/onnxruntime-$(ONNX_OS)-$(ONNX_ARCH)$(if $(ONNX_VARIANT),-$(ONNX_VARIANT),)-$(ONNX_VERSION).tgz \
	  -o sources/onnxruntime/onnxruntime.tgz
	cd sources/onnxruntime && tar -xf onnxruntime.tgz --strip-components=1 && rm onnxruntime.tgz

# Clone and build Sherpa-ONNX from source
sources/sherpa-onnx: sources/onnxruntime
	git clone https://github.com/k2-fsa/sherpa-onnx.git sources/sherpa-onnx
	cd sources/sherpa-onnx && git checkout $(SHERPA_COMMIT)
	mkdir -p sources/sherpa-onnx/build
	cd sources/sherpa-onnx/build && cmake \
	  -DCMAKE_BUILD_TYPE=Release \
	  -DSHERPA_ONNX_ENABLE_GPU=$(SHERPA_GPU) \
	  -DSHERPA_ONNX_ENABLE_TTS=ON \
	  -DSHERPA_ONNX_ENABLE_BINARY=OFF \
	  -DSHERPA_ONNX_ENABLE_PYTHON=OFF \
	  -DSHERPA_ONNX_ENABLE_TESTS=OFF \
	  -DSHERPA_ONNX_ENABLE_C_API=ON \
	  -DBUILD_SHARED_LIBS=ON \
	  -DSHERPA_ONNX_USE_PRE_INSTALLED_ONNXRUNTIME_IF_AVAILABLE=ON \
	  -DONNXRUNTIME_DIR=$(CURRENT_DIR)/sources/onnxruntime \
	  ..
	cd sources/sherpa-onnx/build && make -j$$(nproc)

# Copy libraries to backend-assets
backend-assets/lib: sources/sherpa-onnx sources/onnxruntime
	mkdir -p backend-assets/lib
	cp -rfLv sources/onnxruntime/lib/* backend-assets/lib/
	cp -rfLv sources/sherpa-onnx/build/lib/*.so* backend-assets/lib/ 2>/dev/null || true

# Build Go binary
sherpa-onnx: backend-assets/lib
	CGO_LDFLAGS="$(CGO_LDFLAGS)" \
	CPATH="$(CPATH):$(CURRENT_DIR)/sources/onnxruntime/include/:$(CURRENT_DIR)/sources/sherpa-onnx/sherpa-onnx/c-api/" \
	LIBRARY_PATH=$(CURRENT_DIR)/backend-assets/lib \
	$(GOCMD) build -o sherpa-onnx ./

package:
	bash package.sh

build: sherpa-onnx package

clean:
	rm -rf sherpa-onnx sources/ backend-assets/

test:
	go test -v .
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
- Implement Makefile (download prebuilt ONNX Runtime, git clone + cmake Sherpa-ONNX)
- Verify build via `backend/Dockerfile.golang` (CPU-only)
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
- ✅ Builds successfully via `backend/Dockerfile.golang`
- ✅ Basic functionality test passes with small model
- ✅ Integrated into LocalAI build system

### Phase 2: GPU Acceleration (CUDA)

**Goal**: Add NVIDIA GPU support, validate GPU inference works.

#### Week 4-5: CUDA Build System
- Update Makefile to detect `BUILD_TYPE=cublas` and download GPU ONNX Runtime variant
- Build Sherpa-ONNX with `SHERPA_ONNX_ENABLE_GPU=ON`
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
- Update Makefile to detect `BUILD_TYPE=hipblas` and download ROCm ONNX Runtime variant
- Test on AMD hardware (if available)
- Add ROCm build variant in workflows

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

## Review Observations

### 1. Build Optimization
Using prebuilt ONNX Runtime from upstream eliminates the 30-60+ minute source build. Sherpa-ONNX still needs to be built from source (cmake), but this is much faster (a few minutes).
**Recommendation**: 
- The Makefile uses `sources/` as a download cache; Docker layer caching handles the rest.
- Only re-download/rebuild when versions change.

### 2. Gallery Integration
While this plan focuses on the binary backend, the end-to-end user experience requires model definitions.
**Requirement**:
- Create `gallery` YAML definitions for popular Sherpa-ONNX models (VITS, Matcha, etc.).
- Ensure these definitions map correctly to the backend's expected configuration parameters.

### 3. Runtime Library Resolution
Relying solely on system paths can sometimes be fragile in containerized environments.
**Best Practice**:
- In `run.sh` or the entrypoint, explicitly set `LD_LIBRARY_PATH` to include the backend's library directory.
- This ensures the custom-built shared libraries are found even if standard paths are modified.

### 4. CI Model Selection
Using production-quality models for CI will make tests slow and flaky due to download sizes and inference times.
**Status**: Resolved (Crush review).
- Tiny VITS model: `sherpa-onnx-tiny-vits-330k-237m` (45MB) available at https://k2-fsa.github.io/sherpa/onnx/pretrained_models/.

### 5. Crush AI Review (2026-02-12)

- **Versions Confirmed**: ONNX Runtime v1.24.1 commit valid (latest v1.25.0). Sherpa-ONNX v1.12.23 recent.
- **Build Flags/C API**: All CMake flags, C TTS API (`SherpaOnnxOfflineTtsGenerate`) validated.
- **GPU**: MIGraphX ROCm stable; ROCm EP beta. ArmNN/DirectML deprecated (N/A).
- **Plan**: Solid; no major invalidations. Ready for Phase 1.

### 6. Build Strategy Revision (2026-02-12)

- **ONNX Runtime**: Switched from building from source to downloading prebuilt upstream release tarballs, following the `silero-vad` backend pattern. This eliminates the 30-60+ minute ONNX Runtime compilation.
- **Sherpa-ONNX**: Still built from source via `git clone` + `cmake` inside the Makefile. This is necessary because upstream does not publish prebuilt C/C++ libraries with the exact configuration we need (GPU-enabled, C API, no binaries).
- **Docker**: Uses the existing `backend/Dockerfile.golang` instead of a custom multi-stage Dockerfile. All library acquisition and build logic lives in `backend/go/sherpa-onnx/Makefile`.
- **Pattern**: Follows the same approach as `backend/go/silero-vad/Makefile` for ONNX Runtime, extended with a Sherpa-ONNX build step.

---


## Summary

### Key Points

1. **Pinned Dependencies**:
   - ONNX Runtime: v1.24.1 (prebuilt upstream binaries)
   - Sherpa-ONNX: v1.12.23 (commit `7e227a529be6c383134a358c5744d0eb1cb5ae1f`, built from source)
   - Nested dependencies: Sherpa-ONNX's CMake uses specific tagged releases (reproducible)

2. **Build Strategy**:
   - Download prebuilt ONNX Runtime from upstream GitHub releases (like `silero-vad`)
   - Build Sherpa-ONNX from source via `git clone` + `cmake` in the Makefile
   - All build logic in `backend/go/sherpa-onnx/Makefile`, using existing `backend/Dockerfile.golang`
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

1. ✅ Plan reviewed/approved (Crush AI, 2026-02-12)
2. ✅ Tiny model identified: `sherpa-onnx-tiny-vits-330k-237m`
3. Begin Phase 1: CPU-only TTS implementation
4. Iterate based on feedback

---

## References

- **Sherpa-ONNX Documentation**: https://k2-fsa.github.io/sherpa/onnx/
- **Sherpa-ONNX GitHub**: https://github.com/k2-fsa/sherpa-onnx
- **ONNX Runtime Documentation**: https://onnxruntime.ai/docs/
- **ONNX Runtime Build Guide**: https://onnxruntime.ai/docs/build/
- **LocalAI Backend Documentation**: /home/rich/go/src/github.com/mudler/LocalAI/AGENTS.md
