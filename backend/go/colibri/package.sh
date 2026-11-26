#!/bin/bash
#
# Assemble the backend image contents in package/:
#   colibri        the Go backend (pure Go, no shared libraries)
#   run.sh         entry point; points the backend at everything below
#   colibri-dist/  the upstream colibri release archive, unchanged: the
#                  engines, the coli gateway and its Python modules
#   python/        portable CPython (standard library only) for coli
#   lib/           the OpenMP runtime the engines link, on a Vulkan build
#                  the Vulkan loader and drivers, and on a CUDA build the
#                  CUDA runtime and cuBLAS
#   colibri-dist/ on a CUDA build holds the engines compiled from the
#                  release's source with CUDA=1 in place of the CPU ones
#
# colibri-dist/ and python/ come from `make engine`.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

for d in colibri-dist python; do
	if [ ! -d "$CURDIR/$d" ]; then
		echo "ERROR: $CURDIR/$d is missing, run 'make engine' first" >&2
		exit 1
	fi
done
if [ ! -x "$CURDIR/colibri" ]; then
	echo "ERROR: $CURDIR/colibri is missing, run 'make colibri' first" >&2
	exit 1
fi

rm -rf "$CURDIR/package"
mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/colibri" "$CURDIR/run.sh" "$CURDIR/package/"
chmod +x "$CURDIR/package/run.sh"
cp -af "$CURDIR/colibri-dist" "$CURDIR/package/colibri-dist"
cp -af "$CURDIR/python" "$CURDIR/package/python"
find "$CURDIR/package" -name '.stamp-*' -type f -delete
find "$CURDIR/package" -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true

if [ "$(uname)" = "Darwin" ]; then
	# The macOS engines link Homebrew's libomp by absolute path. Ship it and
	# point the engines at the copy, so the host needs no Homebrew.
	OMP_PREFIX=$(brew --prefix libomp 2>/dev/null || true)
	if [ -z "$OMP_PREFIX" ] || [ ! -f "$OMP_PREFIX/lib/libomp.dylib" ]; then
		brew install libomp
		OMP_PREFIX=$(brew --prefix libomp)
	fi
	cp -fL "$OMP_PREFIX/lib/libomp.dylib" "$CURDIR/package/lib/libomp.dylib"
	chmod u+w "$CURDIR/package/lib/libomp.dylib"
	install_name_tool -id @rpath/libomp.dylib "$CURDIR/package/lib/libomp.dylib"
	codesign --force --sign - "$CURDIR/package/lib/libomp.dylib"
	for f in "$CURDIR"/package/colibri-dist/*; do
		[ -f "$f" ] && [ -x "$f" ] || continue
		dep=$(otool -L "$f" 2>/dev/null | awk '/libomp\.dylib/ {print $1; exit}')
		[ -n "$dep" ] || continue
		install_name_tool -change "$dep" @loader_path/../lib/libomp.dylib "$f"
		codesign --force --sign - "$f"
		echo "==> $(basename "$f"): libomp -> @loader_path/../lib/libomp.dylib"
	done
else
	# The engines link libgomp, which a minimal host image may not have.
	# Only libgomp: libc and the loader stay the host's.
	found=""
	for d in /lib/x86_64-linux-gnu /lib/aarch64-linux-gnu /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu; do
		if [ -f "$d/libgomp.so.1" ]; then
			cp -fL "$d/libgomp.so.1" "$CURDIR/package/lib/libgomp.so.1"
			found=1
			break
		fi
	done
	if [ -z "$found" ]; then
		echo "ERROR: libgomp.so.1 not found in the build image" >&2
		exit 1
	fi
	if [ "${BUILD_TYPE:-}" = "vulkan" ]; then
		# shellcheck source=/dev/null
		source "$REPO_ROOT/scripts/build/package-gpu-libs.sh" "$CURDIR/package/lib"
		package_gpu_libs
		touch "$CURDIR/package/colibri-vulkan"
	fi
	if [ "${BUILD_TYPE:-}" = "cublas" ]; then
		# `make engine-cuda` puts the CUDA engines in colibri-dist and leaves
		# this marker; without it the image would ship the CPU engines.
		if [ ! -f "$CURDIR/package/colibri-dist/colibri-cuda" ]; then
			echo "ERROR: BUILD_TYPE=cublas but colibri-dist has no CUDA engines, run 'make engine BUILD_TYPE=cublas'" >&2
			exit 1
		fi
		# The CUDA runtime and cuBLAS the engines link (the driver comes
		# from the host).
		# shellcheck source=/dev/null
		source "$REPO_ROOT/scripts/build/package-gpu-libs.sh" "$CURDIR/package/lib"
		package_gpu_libs
		mv "$CURDIR/package/colibri-dist/colibri-cuda" "$CURDIR/package/colibri-cuda"
	fi
fi

# Fail the build, not the first request, if coli cannot run.
"$CURDIR/package/python/bin/python3" -I "$CURDIR/package/colibri-dist/coli" --help >/dev/null

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
