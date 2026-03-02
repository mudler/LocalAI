# macOS Support Matrix for LocalAI Backends

## Overview

This document provides a comprehensive overview of macOS support across all LocalAI backends.

## Backend Support Status

### Python Backends

| Backend | CPU | Apple Silicon (MLX) | x86_64 Intel | Notes |
|---------|-----|---------------------|--------------|-------|
| transformers | ✓ | ✓ | ✓ | Full support via PyTorch |
| vllm | ✓ | ✓ | ✓ | Requires specific build flags |
| mlx | - | ✓ | - | Apple Silicon only |
| mlx-audio | - | ✓ | - | Apple Silicon only |
| mlx-vlm | - | ✓ | - | Apple Silicon only |
| faster-whisper | ✓ | ✓ | ✓ | CPU and MPS support |
| coqui | ✓ | ✓ | ✓ | CPU and MPS support |
| whisperx | ✓ | ✓ | ✓ | CPU and MPS support |
| rerankers | ✓ | ✓ | ✓ | Pure Python |
| diffusers | ✓ | ✓ | ✓ | CPU and MPS support |
| chatterbox | ✓ | ✓ | ✓ | CPU and MPS support |
| kokoro | ✓ | ✓ | ✓ | CPU and MPS support |
| qwen-tts | ✓ | ✓ | ✓ | CPU and MPS support |
| qwen-asr | ✓ | ✓ | ✓ | CPU and MPS support |
| nemo | ✓ | ✓ | ✓ | CPU and MPS support |
| pocket-tts | ✓ | ✓ | ✓ | CPU and MPS support |
| moonshine | ✓ | ✓ | ✓ | CPU and MPS support |
| neutts | ✓ | ✓ | ✓ | CPU and MPS support |
| vibevoice | ✓ | ✓ | ✓ | CPU and MPS support |
| outetts | ✓ | ✓ | ✓ | CPU and MPS support |
| kitten-tts | ✓ | ✓ | ✓ | CPU and MPS support |
| ace-step | ✓ | ✓ | ✓ | CPU and MPS support |
| voxcpm | ✓ | ✓ | ✓ | CPU and MPS support |
| rfdetr | ✓ | ✓ | ✓ | CPU and MPS support |

### Go Backends

| Backend | CPU | Apple Silicon | x86_64 Intel | Notes |
|---------|-----|---------------|--------------|-------|
| llama-cpp | ✓ | ✓ | ✓ | Via Homebrew or prebuilt binaries |
| whisper | ✓ | ✓ | ✓ | Full macOS support |
| piper | ✓ | ✓ | ✓ | Full macOS support |
| stablediffusion-ggml | ✓ | ✓ | ✓ | Via Metal backend |
| silero-vad | ✓ | ✓ | ✓ | Full macOS support |
| voxtral | ✓ | ✓ | ✓ | Full macOS support |
| local-store | ✓ | ✓ | ✓ | Full macOS support |
| huggingface | ✓ | ✓ | ✓ | Full macOS support |

## Build Instructions for macOS

### Prerequisites

```bash
# Install Xcode Command Line Tools
xcode-select --install

# Install Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install dependencies
brew install python@3.10 uv cmake protobuf
```

### Building Python Backends

```bash
# For CPU support
cd backend/python/transformers
make transformers

# For Apple Silicon (MLX)
cd backend/python/mlx
make mlx
```

### Building Go Backends

```bash
# Build for macOS
make build-darwin-go-backend
```

## Known Limitations

1. **MLX-specific backends**: Only work on Apple Silicon (M1/M2/M3 chips)
2. **CUDA backends**: Not available on macOS (use MLX for Apple Silicon acceleration)
3. **HIP backends**: Not available on macOS (AMD GPU support limited)

## Testing on macOS

```bash
# Run backend tests
make test

# Run specific backend tests
cd backend/python/transformers
make test
```

## Troubleshooting

### Common Issues

1. **MPS (Metal Performance Shaders) not working**
   - Ensure PyTorch is installed with MPS support
   - Check `torch.backends.mps.is_available()`

2. **MLX backend fails on Intel Mac**
   - MLX only works on Apple Silicon
   - Use CPU or other backends instead

3. **Build fails with "unknown target"**
   - Ensure proper build profile is set
   - Check `BUILD_TYPE` environment variable

## References

- [PyTorch MPS Support](https://pytorch.org/docs/stable/notes/mps.html)
- [MLX Framework](https://ml-explore.github.io/mlx/)
- [llama.cpp macOS](https://github.com/ggerganov/llama.cpp/blob/master/docs/macOS.md)
