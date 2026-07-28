package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"
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
	libName := os.Getenv("MAGPIETTS_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libgomagpiettscpp-fallback.dylib"
		} else {
			libName = "./libgomagpiettscpp-fallback.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppAbiVersion, "magpie_tts_capi_abi_version"},
		{&CppLoad, "magpie_tts_capi_load"},
		{&CppFree, "magpie_tts_capi_free"},
		{&CppSynthesize, "magpie_tts_capi_synthesize"},
		{&CppFreeAudio, "magpie_tts_capi_free_audio"},
		{&CppLastError, "magpie_tts_capi_last_error"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &MagpieTtsCpp{}); err != nil {
		panic(err)
	}
}
