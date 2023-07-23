#!/bin/bash
set -e

cd /build

if [ "$REBUILD" != "false" ]; then
	rm -rf ./local-ai
	ESPEAK_DATA=/build/lib/Linux-$(uname -m)/piper_phonemize/lib/espeak-ng-data make build -j${BUILD_PARALLELISM:-1}
else
	echo "@@@@@"
	echo "Skipping rebuild"
	echo "@@@@@"
	echo "If you are experiencing issues with the pre-compiled builds, try setting REBUILD=true"
	echo "If you are still experiencing issues with the build, try setting CMAKE_ARGS and disable the instructions set as needed:"
	echo 'CMAKE_ARGS="-DLLAMA_F16C=OFF -DLLAMA_AVX512=OFF -DLLAMA_AVX2=OFF -DLLAMA_FMA=OFF"'
	echo "see the documentation at: https://localai.io/basics/build/index.html"
	echo "Note: See also https://github.com/go-skynet/LocalAI/issues/288"
	echo "@@@@@"
	echo "CPU info:"
	grep -e "model\sname" /proc/cpuinfo | head -1
	grep -e "flags" /proc/cpuinfo | head -1
	if grep -q -e "\savx\s" /proc/cpuinfo ; then
		echo "CPU:    AVX    found OK"
	else
		echo "CPU: no AVX    found"
	fi
	if grep -q -e "\savx2\s" /proc/cpuinfo ; then
		echo "CPU:    AVX2   found OK"
	else
		echo "CPU: no AVX2   found"
	fi
	if grep -q -e "\savx512" /proc/cpuinfo ; then
		echo "CPU:    AVX512 found OK"
	else
		echo "CPU: no AVX512 found"
	fi
	echo "@@@@@"
fi

./local-ai "$@"
