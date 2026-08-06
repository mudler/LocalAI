#!/bin/bash
#
# Bundle the nemo-speech-cpp-grpc binary, the five nemo_speech shared objects,
# the text-normalization stack on a WITH_NORM build, the core runtime libs
# (libc/libstdc++/libgomp + ld.so) and the GPU runtime for the active BUILD_TYPE
# so the package is self-contained. Mirrors backend/go/whisper/package.sh;
# run.sh routes the (CGO_ENABLED=0) binary through lib/ld.so so the packaged
# libc is used instead of the host's.
#
# Five, not three: ASR and NMT each ship a thin _c ABI shim plus the
# implementation DSO it depends on, while TTS ships one object with no _c
# suffix at all.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/nemo-speech-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# The runtime ships three C ABI shared objects, not one. All three are
# required: main.go dlopens them eagerly, so a package missing any of them
# fails at startup. ASR and NMT expose the ABI through a dedicated _c library;
# TTS compiles its c_api into libnemo_speech_tts itself and has no _c variant,
# hence the asymmetric list. purego.Dlopen resolves them via the
# NEMO_SPEECH_*_LIBRARY paths that run.sh points at lib/.
#
# libnemo_speech_asr and libnemo_speech_nmt are in the list because the matching
# _c shims carry a DT_NEEDED on them: dlopen of the shim fails without the
# implementation DSO alongside it.
for lib in libnemo_speech_asr_c libnemo_speech_asr libnemo_speech_tts libnemo_speech_nmt_c libnemo_speech_nmt; do
	cp -avf "$CURDIR"/${lib}.so* "$CURDIR/package/lib/" 2>/dev/null || true
	cp -avf "$CURDIR"/${lib}*.dylib "$CURDIR/package/lib/" 2>/dev/null || true
	if ! ls "$CURDIR"/package/lib/${lib}.* >/dev/null 2>&1; then
		echo "ERROR: ${lib} shared library not found in $CURDIR, run 'make' first" >&2
		exit 1
	fi
done

# Text normalization (WITH_NORM=ON, Linux only) links Sparrowhawk and OpenFST
# into libnemo_speech_asr.so. Those live in a project-local prefix that the
# Makefile stages here, so anything staged that is not a nemo_speech object is
# part of that stack. Absent on a WITH_NORM=OFF build, which is why this is a
# glob that tolerates no matches rather than a required list.
shopt -s nullglob
for so in "$CURDIR"/*.so "$CURDIR"/*.so.* "$CURDIR"/*.dylib; do
	case "$(basename "$so")" in
		libnemo_speech_*) continue ;;
	esac
	cp -avf "$so" "$CURDIR/package/lib/"
done
shopt -u nullglob

# Detect architecture and copy the core runtime libs the shared objects link
# against, plus the matching dynamic loader as lib/ld.so.
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Dependency-closure guard.
#
# The lists above are maintained by hand, and the WITH_NORM build in particular
# pulls in transitive dependencies nobody enumerated: Sparrowhawk drags in
# protobuf, re2 and absl, none of which package-system-libs.sh provides. Rather
# than hard-code that set, walk the DT_NEEDED entries of everything staged and
# copy whatever is still unresolved. On a WITH_NORM=OFF build the closure is
# already complete, so this copies nothing.
#
# Skipped deliberately: the core runtime set that package-system-libs.sh owns,
# and the GPU stack that package-gpu-libs.sh owns.
shopt -s nullglob
staged_libs=("$CURDIR"/package/lib/*.so*)
shopt -u nullglob

if [ "$(uname)" != "Darwin" ] && [ "${#staged_libs[@]}" -gt 0 ]; then
	# No silent skip. If the closure cannot be checked, the package cannot be
	# shown to be complete, and shipping an unverified one is the failure this
	# guard exists to prevent.
	if command -v readelf >/dev/null 2>&1; then
		read_needed() { readelf -d "$1" 2>/dev/null | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p'; }
	elif command -v objdump >/dev/null 2>&1; then
		read_needed() { objdump -p "$1" 2>/dev/null | awk '$1 == "NEEDED" { print $2 }'; }
	else
		echo "ERROR: neither readelf nor objdump is available, so the dependency" >&2
		echo "       closure of ${#staged_libs[@]} staged libraries cannot be verified." >&2
		echo "       Install binutils in the build image; refusing to ship an" >&2
		echo "       unverified package." >&2
		exit 1
	fi

	is_provided() {
		case "$1" in
			ld-linux*|libc.so.6|libstdc++.so.6|libgcc_s.so.1|libm.so.6|libgomp.so.1) return 0 ;;
			libdl.so.2|librt.so.1|libpthread.so.0) return 0 ;;
			libcuda*|libcudart*|libcublas*|libcublasLt*|libnvrtc*|libnvidia*) return 0 ;;
			libamdhip*|libhsa*|librocm*|libze_*|libOpenCL*|libvulkan*) return 0 ;;
		esac
		[ -e "$CURDIR/package/lib/$1" ]
	}

	# Walk until the staged set stops growing. The glob below expands once per
	# pass, so each pass advances the closure by exactly one dependency level;
	# a copied library can itself pull in new dependencies.
	#
	# CLOSURE_MAX_PASSES is a runaway guard, not a depth limit. Exhausting it
	# means the walk never converged and the package is therefore incomplete,
	# which has to fail the build: a fixed pass count that just falls out of the
	# loop would silently ship a package missing its deepest libraries, and
	# libnemo_speech_asr -> sparrowhawk -> protobuf -> absl already runs several
	# levels deep.
	CLOSURE_MAX_PASSES="${CLOSURE_MAX_PASSES:-64}"
	converged=0
	for (( pass=1; pass<=CLOSURE_MAX_PASSES; pass++ )); do
		missing=0
		for so in "$CURDIR"/package/lib/*.so*; do
			[ -f "$so" ] || continue
			for need in $(read_needed "$so"); do
				# Written as an if rather than "is_provided && continue" so a
				# false return cannot trip set -e via the AND-list exit status.
				if is_provided "$need"; then
					continue
				fi
				# Resolve against the staging dir first, then the system loader.
				src="$(LD_LIBRARY_PATH="$CURDIR:$CURDIR/package/lib:${LD_LIBRARY_PATH:-}" \
					ldd "$so" 2>/dev/null | awk -v n="$need" '$1 == n { print $3 }' | head -1)"
				if [ -z "$src" ] || [ ! -e "$src" ]; then
					echo "ERROR: $(basename "$so") needs $need and it could not be resolved." >&2
					echo "       The packaged backend would fail to dlopen at runtime." >&2
					exit 1
				fi
				cp -aLvf "$src" "$CURDIR/package/lib/$need"
				missing=1
			done
		done
		if [ "$missing" -eq 0 ]; then
			converged=1
			break
		fi
	done

	if [ "$converged" -ne 1 ]; then
		echo "ERROR: the dependency closure was still growing after" >&2
		echo "       $CLOSURE_MAX_PASSES passes, so the package is incomplete and" >&2
		echo "       would fail to dlopen at runtime. Refusing to ship it." >&2
		exit 1
	fi
fi

# Package GPU libraries (CUDA/ROCm/Intel/Vulkan loader + ICDs + drivers)
# based on BUILD_TYPE so the backend can reach the GPU without the runtime
# base image shipping those drivers.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"
