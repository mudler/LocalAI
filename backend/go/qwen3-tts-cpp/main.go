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
	libName := os.Getenv("QWEN3TTS_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libgoqwen3ttscpp-fallback.dylib"
		} else {
			libName = "./libgoqwen3ttscpp-fallback.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoad, "qt3_load"},
		{&CppTTS, "qt3_tts"},
		{&CppTTSStream, "qt3_tts_stream"},
		{&CppPCMFree, "qt3_pcm_free"},
		{&CppUnload, "qt3_unload"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &Qwen3TtsCpp{}); err != nil {
		panic(err)
	}
}
