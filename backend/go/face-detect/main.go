package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads libfacedetect.so via purego and registers the flat C-API entry points
// declared in facedetect_capi.h. The library name can be overridden with
// FACEDETECT_LIBRARY (mirrors the VOICEDETECT_LIBRARY / PARAKEET_LIBRARY
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
	libName := os.Getenv("FACEDETECT_LIBRARY")
	if libName == "" {
		libName = "libfacedetect.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("face-detect: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to facedetect_capi.h. char*/float* returns are registered as
	// uintptr so the raw pointer can be freed via the matching capi free fn.
	libFuncs := []LibFuncs{
		{&CppAbiVersion, "facedetect_capi_abi_version"},
		{&CppLoad, "facedetect_capi_load"},
		{&CppFree, "facedetect_capi_free"},
		{&CppLastError, "facedetect_capi_last_error"},
		{&CppFreeString, "facedetect_capi_free_string"},
		{&CppFreeVec, "facedetect_capi_free_vec"},
		{&CppEmbedPath, "facedetect_capi_embed_path"},
		{&CppEmbedRGB, "facedetect_capi_embed_rgb"},
		{&CppDetectJSON, "facedetect_capi_detect_path_json"},
		{&CppVerifyPaths, "facedetect_capi_verify_paths"},
		{&CppAnalyzeJSON, "facedetect_capi_analyze_path_json"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	fmt.Fprintf(os.Stderr, "[face-detect] ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &FaceDetect{}); err != nil {
		panic(err)
	}
}
