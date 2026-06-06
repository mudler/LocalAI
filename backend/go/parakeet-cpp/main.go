package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads libparakeet.so via purego and registers the flat C-API entry
// points declared in parakeet_capi.h. The library name can be overridden
// with PARAKEET_LIBRARY (mirrors the WHISPER_LIBRARY / VIBEVOICECPP_LIBRARY
// convention in the sibling backends); the default looks for the .so next
// to this binary.
import (
	"flag"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

type LibFuncs struct {
	FuncPtr any
	Name    string
}

func main() {
	libName := os.Getenv("PARAKEET_LIBRARY")
	if libName == "" {
		libName = "libparakeet.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("parakeet-cpp: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to parakeet_capi.h. The C-API returns malloc'd char*
	// buffers from transcribe_*; we register those as uintptr so we get
	// the raw pointer back and can call parakeet_capi_free_string on it
	// (purego's string return would copy and forget the original pointer,
	// leaking it on every call).
	libFuncs := []LibFuncs{
		{&CppAbiVersion, "parakeet_capi_abi_version"},
		{&CppLoad, "parakeet_capi_load"},
		{&CppFree, "parakeet_capi_free"},
		{&CppTranscribePath, "parakeet_capi_transcribe_path"},
		{&CppTranscribePathJSON, "parakeet_capi_transcribe_path_json"},
		{&CppStreamBegin, "parakeet_capi_stream_begin"},
		{&CppStreamFeed, "parakeet_capi_stream_feed"},
		{&CppStreamFinalize, "parakeet_capi_stream_finalize"},
		{&CppStreamFree, "parakeet_capi_stream_free"},
		{&CppFreeString, "parakeet_capi_free_string"},
		{&CppLastError, "parakeet_capi_last_error"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	// The batched-JSON entry point exists only in newer libparakeet.so (ABI >= 2).
	// Probe with Dlsym and register only if present, so the backend still loads
	// against an older library (it falls back to per-request transcription).
	if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_pcm_batch_json"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppTranscribePcmBatchJSON, lib, "parakeet_capi_transcribe_pcm_batch_json")
	}

	// Per-request language variants (multilingual nemotron). Same probe pattern:
	// present only in libparakeet.so built with multilingual support, so the
	// backend still loads against an older library and falls back to the
	// non-lang batched + streaming entry points (model default / "auto").
	if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_pcm_batch_json_lang"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppTranscribePcmBatchJSONLang, lib, "parakeet_capi_transcribe_pcm_batch_json_lang")
	}
	if sym, err := purego.Dlsym(lib, "parakeet_capi_stream_begin_lang"); err == nil && sym != 0 {
		purego.RegisterLibFunc(&CppStreamBeginLang, lib, "parakeet_capi_stream_begin_lang")
	}

	fmt.Fprintf(os.Stderr, "[parakeet-cpp] ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &ParakeetCpp{}); err != nil {
		panic(err)
	}
}
