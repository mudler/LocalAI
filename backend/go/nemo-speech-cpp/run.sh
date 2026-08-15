#!/bin/bash
set -e

CURDIR=$(dirname "$(realpath "$0")")

# The runtime splits its C ABI across three shared objects, so each gets its
# own override variable. main.go reads exactly these names.
if [ "$(uname)" = "Darwin" ]; then
	export DYLD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${DYLD_LIBRARY_PATH:-}"
	export NEMO_SPEECH_ASR_LIBRARY="$CURDIR/lib/libnemo_speech_asr_c.dylib"
	export NEMO_SPEECH_TTS_LIBRARY="$CURDIR/lib/libnemo_speech_tts.dylib"
	export NEMO_SPEECH_NMT_LIBRARY="$CURDIR/lib/libnemo_speech_nmt_c.dylib"
else
	export LD_LIBRARY_PATH="$CURDIR/lib:"$CURDIR":${LD_LIBRARY_PATH:-}"
	export NEMO_SPEECH_ASR_LIBRARY="$CURDIR/lib/libnemo_speech_asr_c.so"
	export NEMO_SPEECH_TTS_LIBRARY="$CURDIR/lib/libnemo_speech_tts.so"
	export NEMO_SPEECH_NMT_LIBRARY="$CURDIR/lib/libnemo_speech_nmt_c.so"
fi

# If a self-contained ld.so was packaged, route through it so the
# packaged libc / libstdc++ are used instead of the host's (matches the
# whisper backend's runtime layout). Linux only.
if [ -f "$CURDIR/lib/ld.so" ]; then
	echo "Using lib/ld.so"
	exec "$CURDIR/lib/ld.so" "$CURDIR/nemo-speech-cpp-grpc" "$@"
fi

exec "$CURDIR/nemo-speech-cpp-grpc" "$@"
