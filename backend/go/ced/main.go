package main

// ced sound-classification backend. Started internally by LocalAI: one gRPC
// server per loaded model. Loads libced.so via purego and registers the flat
// C-API declared in ced_capi.h. The library name can be overridden with
// CED_LIBRARY (mirrors PARAKEET_LIBRARY / WHISPER_LIBRARY); the default looks
// for the .so next to this binary.
//
// SKETCH: requires `make protogen-go` after the backend.proto SoundDetection
// addition, and a built libced.so (see Makefile). See DESIGN.md.
import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var addr = flag.String("addr", "localhost:50051", "the address to connect to")

type libFunc struct {
	ptr  any
	name string
}

func main() {
	libName := os.Getenv("CED_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "libced.dylib"
		} else {
			libName = "libced.so"
		}
	}
	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("ced: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to ced_capi.h. char*-returning functions are declared uintptr
	// so we can free the same pointer with ced_capi_free_string after copying
	// (purego's string return would copy and leak the original).
	for _, lf := range []libFunc{
		{&CppAbiVersion, "ced_capi_abi_version"},
		{&CppLoad, "ced_capi_load"},
		{&CppFree, "ced_capi_free"},
		{&CppLastError, "ced_capi_last_error"},
		{&CppNumClasses, "ced_capi_num_classes"},
		{&CppSampleRate, "ced_capi_sample_rate"},
		{&CppClassifyPathJSON, "ced_capi_classify_path_json"},
		{&CppClassifyPcmJSON, "ced_capi_classify_pcm_json"},
		{&CppFreeString, "ced_capi_free_string"},
	} {
		purego.RegisterLibFunc(lf.ptr, lib, lf.name)
	}

	fmt.Fprintf(os.Stderr, "[ced] ABI=%d\n", CppAbiVersion())
	flag.Parse()
	if err := grpc.StartServer(*addr, &Ced{}); err != nil {
		panic(err)
	}
}
