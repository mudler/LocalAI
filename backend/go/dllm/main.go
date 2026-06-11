package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads libdllm.so via purego and registers the 9-symbol flat C-ABI
// declared in dllm.cpp's include/dllm_capi.h (ABI v1). The library name can
// be overridden with DLLM_LIBRARY (mirrors the PARAKEET_LIBRARY /
// WHISPER_LIBRARY convention in the sibling backends); the default looks
// for the .so next to this binary (run.sh puts the package dir on
// LD_LIBRARY_PATH).
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

// loadCAPI dlopens libName and binds the 9 dllm_capi_* entry points 1:1 to
// dllm_capi.h, so an `nm libdllm.so | grep dllm_capi` is enough to spot
// drift. Shared with the test suite (ensureLibLoaded), which drives the
// bridge without the gRPC server.
//
// The C-ABI returns malloc'd char* buffers from tokenize_json/generate; we
// register those as uintptr so we get the raw pointer back and can call
// dllm_capi_free_string on it (purego's string return would copy and forget
// the original pointer, leaking it on every call). last_error returns a
// BORROWED pointer instead, so it is registered as a plain string: purego
// copies it and nothing must be freed.
func loadCAPI(libName string) error {
	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("dllm: dlopen %q: %w", libName, err)
	}

	libFuncs := []LibFuncs{
		{&cppAbiVersion, "dllm_capi_abi_version"},
		{&cppLoad, "dllm_capi_load"},
		{&cppFree, "dllm_capi_free"},
		{&cppLastError, "dllm_capi_last_error"},
		{&cppFreeString, "dllm_capi_free_string"},
		{&cppTokenizeJSON, "dllm_capi_tokenize_json"},
		{&cppGenerate, "dllm_capi_generate"},
		{&cppGenerateStream, "dllm_capi_generate_stream"},
		{&cppCancel, "dllm_capi_cancel"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}
	return nil
}

func main() {
	libName := os.Getenv("DLLM_LIBRARY")
	if libName == "" {
		libName = "libdllm.so"
	}

	if err := loadCAPI(libName); err != nil {
		panic(err)
	}

	// Hard-fail on an ABI mismatch: the flat-pointer bindings above would
	// otherwise misbehave silently against a future libdllm.so.
	if v := cAbiVersion(); v != dllmABIVersion {
		panic(fmt.Errorf("dllm: libdllm.so ABI=%d, this backend speaks ABI=%d", v, dllmABIVersion))
	}
	fmt.Fprintf(os.Stderr, "[dllm] ABI=%d\n", cAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &Dllm{}); err != nil {
		panic(err)
	}
}
