#!/bin/bash
set -ex

# Get the absolute current dir where the script is located
CURDIR=$(dirname "$(realpath "$0")")

cd /

echo "CPU info:"
if [ "$(uname)" != "Darwin" ]; then
	grep -e "model\sname" /proc/cpuinfo | head -1
	grep -e "flags" /proc/cpuinfo | head -1
fi

if [ "$(uname)" = "Darwin" ]; then
	# macOS: single dylib variant (Metal or Accelerate)
	LIBRARY="$CURDIR/libgocrispasr-fallback.dylib"
	export DYLD_LIBRARY_PATH="$CURDIR"/lib:$DYLD_LIBRARY_PATH
else
	LIBRARY="$CURDIR/libgocrispasr-fallback.so"

	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
		if [ -e "$CURDIR"/libgocrispasr-avx.so ]; then
			LIBRARY="$CURDIR/libgocrispasr-avx.so"
		fi
	fi

	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
		if [ -e "$CURDIR"/libgocrispasr-avx2.so ]; then
			LIBRARY="$CURDIR/libgocrispasr-avx2.so"
		fi
	fi

	# Check avx 512
	if grep -q -e "\savx512f\s" /proc/cpuinfo ; then
		echo "CPU:    AVX512F found OK"
		if [ -e "$CURDIR"/libgocrispasr-avx512.so ]; then
			LIBRARY="$CURDIR/libgocrispasr-avx512.so"
		fi
	fi

	export LD_LIBRARY_PATH="$CURDIR"/lib:$LD_LIBRARY_PATH
fi

export CRISPASR_LIBRARY=$LIBRARY

# Point piper's espeak-ng phonemizer at the bundled voice data. The variable
# names the directory CONTAINING espeak-ng-data (package.sh drops it next to
# this script). Harmless when espeak-ng wasn't bundled.
export CRISPASR_ESPEAK_DATA_PATH="$CURDIR"

# If there is a lib/ld.so, use it
if [ -f "$CURDIR"/lib/ld.so ]; then
	echo "Using lib/ld.so"
	echo "Using library: $LIBRARY"
	exec "$CURDIR"/lib/ld.so "$CURDIR"/crispasr "$@"
fi

echo "Using library: $LIBRARY"
exec "$CURDIR"/crispasr "$@"
