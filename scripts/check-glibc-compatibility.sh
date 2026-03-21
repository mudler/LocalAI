#!/bin/bash
# GLIBC Compatibility Check Script for LocalAI CUDA Backends
# This script helps diagnose GLIBC version issues with CUDA backends

set -e

echo "=== LocalAI GLIBC Compatibility Check ==="
echo

# Check if ldd is available
if ! command -v ldd &> /dev/null; then
    echo "ERROR: ldd command not found. Cannot determine GLIBC version."
    exit 1
fi

# Get system GLIBC version
echo "1. System GLIBC Version:"
GLIBC_VERSION=$(ldd --version 2>&1 | head -n 1 | grep -oP '\d+\.\d+' || echo "Unknown")
echo "   GLIBC Version: $GLIBC_VERSION"
echo

# Get backend path (if provided or use default)
BACKEND_PATH="${1:-/opt/localai/backends}"
if [ ! -d "$BACKEND_PATH" ]; then
    echo "2. Backend Path: $BACKEND_PATH (not found)"
    echo "   Skipping backend GLIBC check."
else
    echo "2. Backend GLIBC Compatibility:"
    
    # Find CUDA backends
    CUDA_BACKENDS=$(find "$BACKEND_PATH" -name "*cuda*" -type d 2>/dev/null || true)
    
    if [ -z "$CUDA_BACKENDS" ]; then
        echo "   No CUDA backends found in $BACKEND_PATH"
    else
        for backend in $CUDA_BACKENDS; do
            echo "   Checking: $backend"
            
            # Find libc.so.6 in backend
            BACKEND_LIBC=$(find "$backend" -name "libc.so.6" 2>/dev/null | head -n 1 || true)
            
            if [ -n "$BACKEND_LIBC" ]; then
                BACKEND_GLIBC=$(ldd "$BACKEND_LIBC" 2>&1 | head -n 1 | grep -oP '\d+\.\d+' || echo "Unknown")
                echo "     Backend GLIBC: $BACKEND_GLIBC"
                
                # Compare versions (simplified comparison)
                SYS_MAJOR=$(echo $GLIBC_VERSION | cut -d. -f1)
                SYS_MINOR=$(echo $GLIBC_VERSION | cut -d. -f2)
                BACK_MAJOR=$(echo $BACKEND_GLIBC | cut -d. -f1)
                BACK_MINOR=$(echo $BACKEND_GLIBC | cut -d. -f2)
                
                if [ "$BACK_MAJOR" -gt "$SYS_MAJOR" ] || ([ "$BACK_MAJOR" -eq "$SYS_MAJOR" ] && [ "$BACK_MINOR" -gt "$SYS_MINOR" ]); then
                    echo "     ⚠️  WARNING: Backend requires GLIBC $BACKEND_GLIBC, system has $GLIBC_VERSION"
                    echo "     This may cause 'version not found' errors."
                    echo "     Recommendation: Use Docker container or rebuild backend."
                else
                    echo "     ✅ Compatible"
                fi
            fi
        done
    fi
    echo
fi

# Check for known problematic symbols
echo "3. Checking for GLIBC symbol issues:"
if [ -d "$BACKEND_PATH" ]; then
    # Look for the specific error pattern mentioned in issue #9093
    MISSING_SYMBOLS=$(find "$BACKEND_PATH" -name "libc.so.6" -exec strings {} \; 2>/dev/null | grep -c "GLIBC_ABI_DT_X86_64_PLT" || echo "0")
    if [ "$MISSING_SYMBOLS" -gt 0 ]; then
        echo "   ⚠️  Found GLIBC_ABI_DT_X86_64_PLT symbol references"
        echo "   This symbol requires GLIBC 2.34+"
    else
        echo "   No problematic symbol references detected"
    fi
fi
echo

# Distribution info
echo "4. Distribution Information:"
if [ -f /etc/os-release ]; then
    DISTRO=$(grep "^NAME=" /etc/os-release | cut -d'"' -f2)
    VERSION=$(grep "^VERSION=" /etc/os-release | cut -d'"' -f2)
    echo "   OS: $DISTRO $VERSION"
else
    echo "   Unable to determine distribution"
fi
echo

# Recommendations
echo "5. Recommendations:"
SYS_MAJOR=$(echo $GLIBC_VERSION | cut -d. -f1)
SYS_MINOR=$(echo $GLIBC_VERSION | cut -d. -f2)

if [ "$SYS_MAJOR" -lt 2 ] || ([ "$SYS_MAJOR" -eq 2 ] && [ "$SYS_MINOR" -lt 35 ]); then
    echo "   ⚠️  Your system has an older GLIBC version (< 2.35)"
    echo "   Recommended actions:"
    echo "   - Use the Docker container: docker run --gpus all ghcr.io/mudler/localai:latest-cuda13"
    echo "   - Or rebuild the CUDA backend on your system"
    echo "   - Or use the CPU backend instead"
else
    echo "   ✅ Your GLIBC version appears compatible with Ubuntu 24.04 builds"
    echo "   If you still experience issues, try:"
    echo "   - Using the Docker container for consistent environment"
    echo "   - Checking CUDA driver compatibility"
fi

echo
echo "=== Check Complete ==="
