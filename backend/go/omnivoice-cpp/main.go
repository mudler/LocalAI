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
	libName := os.Getenv("OMNIVOICE_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libgomnivoicecpp-fallback.dylib"
		} else {
			libName = "./libgomnivoicecpp-fallback.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoad, "omni_load"},
		{&CppTTS, "omni_tts"},
		{&CppTTSStream, "omni_tts_stream"},
		{&CppPCMFree, "omni_pcm_free"},
		{&CppUnload, "omni_unload"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &OmnivoiceCpp{}); err != nil {
		panic(err)
	}
}
