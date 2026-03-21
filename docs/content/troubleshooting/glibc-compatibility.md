# GLIBC Compatibility for CUDA Backends

## Issue Description

Users may encounter GLIBC version incompatibility errors when running CUDA backends on systems with different GLIBC versions than the build environment.

### Example Error

```
/home/localai/backends/cuda13-llama-cpp/lib/libc.so.6: version `GLIBC_ABI_DT_X86_64_PLT` not found
```

This error indicates that the backend binary was compiled against a GLIBC version that includes symbols not available on the target system.

## Root Cause

The LocalAI CUDA backends are built using **Ubuntu 24.04** which ships with **GLIBC 2.39**. When running on systems with:
- Older GLIBC versions (e.g., Ubuntu 20.04 with GLIBC 2.31)
- Different distributions (e.g., Debian Sid, Alpine with musl libc)
- Custom GLIBC builds

The binary may fail to load due to symbol version mismatches.

## Supported GLIBC Versions

| Distribution | Version | GLIBC Version | Supported |
|--------------|---------|---------------|-----------|
| Ubuntu | 24.04 (Noble) | 2.39 | ✅ Officially supported |
| Ubuntu | 22.04 (Jammy) | 2.35 | ✅ Compatible |
| Ubuntu | 20.04 (Focal) | 2.31 | ⚠️ May have issues |
| Debian | Stable (12) | 2.36 | ✅ Compatible |
| Debian | Sid (Testing) | 2.39+ | ⚠️ May have issues with bleeding edge |
| Alpine | Latest | musl libc | ❌ Not supported |

## Solutions

### Option 1: Use Pre-built Docker Container

The recommended approach is to run LocalAI in a Docker container that matches the build environment:

```bash
docker run --gpus all -p 8080:8080 ghcr.io/mudler/localai:latest-cuda13
```

### Option 2: Rebuild Backend Against Compatible GLIBC

If you must run on a system with an older GLIBC, rebuild the backend:

```bash
# Clone the repository
git clone https://github.com/mudler/LocalAI.git
cd LocalAI

# Build the CUDA13 backend
make backends/cuda13-llama-cpp

# Or build with specific base image for older GLIBC
# (requires modifying the Dockerfile to use an older Ubuntu base)
```

### Option 3: Use CPU Backend

If GPU acceleration is not strictly required, use the CPU backend:

```yaml
models:
  - name: "llama-cpp"
    model: "path/to/model.gguf"
    backend: "cpu-llama-cpp"
```

### Option 4: Static Linking (Advanced)

For advanced users, you can attempt to statically link the backend against a compatible GLIBC, but this requires significant modification to the build process.

## Detection

To check your system's GLIBC version:

```bash
ldd --version
# or
/lib/x86_64-linux-gnu/libc.so.6
```

To check what GLIBC symbols a binary requires:

```bash
ldd /path/to/backend/lib/libc.so.6
strings /path/to/backend/lib/libc.so.6 | grep GLIBC
```

## Reporting Issues

When reporting GLIBC-related issues, please include:

1. Your distribution and version
2. GLIBC version (`ldd --version`)
3. CUDA driver and toolkit versions
4. The exact error message
5. Whether you're using Docker or native installation

## Known Workarounds

### For Debian Sid Users

Debian Sid may have rolling GLIBC updates that cause compatibility issues. Workarounds:

1. Use the Docker container instead of native installation
2. Pin your GLIBC version if possible
3. Build the backend locally on your system

### For Alpine Linux Users

Alpine uses musl libc which is incompatible with glibc binaries. You must:
1. Use a glibc compatibility layer (e.g., `gcompat`)
2. Or run LocalAI in a Docker container with glibc-based base image

## References

- [GLIBC Release Notes](https://www.gnu.org/software/libc/news.html)
- [Ubuntu 24.04 Release Notes](https://wiki.ubuntu.com/NobleNumbat/ReleaseNotes)
- [CUDA System Requirements](https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html#system-requirements)
