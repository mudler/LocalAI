package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads libvoicedetect.so via purego and registers the flat C-API entry points
// declared in voicedetect_capi.h. The library name can be overridden with
// VOICEDETECT_LIBRARY (mirrors the PARAKEET_LIBRARY / OMNIVOICE_LIBRARY
// convention in the sibling backends); the default looks for the .so next to
// this binary (resolved via LD_LIBRARY_PATH by run.sh).
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
	libName := os.Getenv("VOICEDETECT_LIBRARY")
	if libName == "" {
		libName = "libvoicedetect.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("voice-detect: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to voicedetect_capi.h. char*/float* returns are registered as
	// uintptr so the raw pointer can be freed via the matching capi free fn.
	libFuncs := []LibFuncs{
		{&CppAbiVersion, "voicedetect_capi_abi_version"},
		{&CppLoad, "voicedetect_capi_load"},
		{&CppFree, "voicedetect_capi_free"},
		{&CppLastError, "voicedetect_capi_last_error"},
		{&CppFreeString, "voicedetect_capi_free_string"},
		{&CppFreeVec, "voicedetect_capi_free_vec"},
		{&CppEmbedPath, "voicedetect_capi_embed_path"},
		{&CppEmbedPCM, "voicedetect_capi_embed_pcm"},
		{&CppVerifyPaths, "voicedetect_capi_verify_paths"},
		{&CppAnalyzeJSON, "voicedetect_capi_analyze_path_json"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	fmt.Fprintf(os.Stderr, "[voice-detect] ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &VoiceDetect{}); err != nil {
		panic(err)
	}
}
