package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads the moss-transcribe shared library via purego and registers the flat
// C-API entry points declared in moss_transcribe_capi.h. The library name can
// be overridden with MOSS_TRANSCRIBE_LIBRARY (mirrors the WHISPER_LIBRARY /
// PARAKEET_LIBRARY convention in the sibling backends); the default looks next
// to this binary for libmoss-transcribe.so on Linux and
// libmoss-transcribe.dylib on macOS.
import (
	"flag"
	"fmt"
	"os"
	"runtime"

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
	libName := os.Getenv("MOSS_TRANSCRIBE_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "libmoss-transcribe.dylib"
		} else {
			libName = "libmoss-transcribe.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("moss-transcribe-cpp: dlopen %q: %w", libName, err))
	}

	// Bound 1:1 to moss_transcribe_capi.h. The transcribe_* entry points return
	// a malloc'd char* the caller owns; we register those as uintptr so we get
	// the raw pointer back and can call moss_transcribe_capi_free_string on it
	// (purego's string return would copy and forget the original pointer,
	// leaking it on every call).
	libFuncs := []LibFuncs{
		{&CppAbiVersion, "moss_transcribe_capi_abi_version"},
		{&CppLoad, "moss_transcribe_capi_load"},
		{&CppFree, "moss_transcribe_capi_free"},
		{&CppTranscribePath, "moss_transcribe_capi_transcribe_path"},
		{&CppTranscribePcm, "moss_transcribe_capi_transcribe_pcm"},
		{&CppFreeString, "moss_transcribe_capi_free_string"},
		{&CppLastError, "moss_transcribe_capi_last_error"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	fmt.Fprintf(os.Stderr, "[moss-transcribe-cpp] ABI=%d\n", CppAbiVersion())

	flag.Parse()

	if err := grpc.StartServer(*addr, &MossTranscribeCpp{}); err != nil {
		panic(err)
	}
}
