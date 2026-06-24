package main

// Started internally by LocalAI, one gRPC server per loaded model.
//
// Binds NVIDIA NeMo-Speech.cpp through purego. The runtime splits its C ABI
// across three shared objects: asr (which also exports the diarization
// symbols), tts, and nmt. Library names can be overridden with
// NEMO_SPEECH_ASR_LIBRARY / _TTS_LIBRARY / _NMT_LIBRARY, mirroring the
// PARAKEET_LIBRARY convention in the sibling backends.
//
// The naming is asymmetric on purpose: upstream links a dedicated
// libnemo_speech_asr_c / libnemo_speech_nmt_c around a private C++ core, but
// compiles the TTS c_api straight into libnemo_speech_tts and only aliases the
// nemo_speech_tts_c CMake target, so there is no libnemo_speech_tts_c on disk.
import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var addr = flag.String("addr", "localhost:50051", "the address to connect to")

// libSuffix is the platform's shared-object extension.
func libSuffix() string {
	if runtime.GOOS == "darwin" {
		return ".dylib"
	}
	return ".so"
}

// libraryName resolves an override env var, falling back to the platform name.
func libraryName(envVar, base string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return base + libSuffix()
}

func main() {
	flag.Parse()

	if err := openLibraries(); err != nil {
		panic(err)
	}

	if err := grpc.StartServer(*addr, &NemoSpeech{}); err != nil {
		panic(err)
	}
}

// openLibraries dlopens the three C ABI shared objects. All three are opened
// eagerly so a packaging mistake fails at startup with a clear message rather
// than at first inference of one particular family.
func openLibraries() error {
	for _, l := range []struct {
		env  string
		base string
		dst  *uintptr
	}{
		{"NEMO_SPEECH_ASR_LIBRARY", "libnemo_speech_asr_c", &asrLib},
		{"NEMO_SPEECH_TTS_LIBRARY", "libnemo_speech_tts", &ttsLib},
		{"NEMO_SPEECH_NMT_LIBRARY", "libnemo_speech_nmt_c", &nmtLib},
	} {
		name := libraryName(l.env, l.base)
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return fmt.Errorf("nemo-speech-cpp: dlopen %q: %w", name, err)
		}
		*l.dst = h
	}
	return registerSymbols()
}
