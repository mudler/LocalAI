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
	libName := os.Getenv("MOSSTTS_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libgomosstts-cpp-fallback.dylib"
		} else {
			libName = "./libgomosstts-cpp-fallback.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoad, "mtl_load"},
		{&CppTTS, "mtl_tts"},
		{&CppPCMFree, "mtl_pcm_free"},
		{&CppUnload, "mtl_unload"},
	}
	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &MossTtsCpp{}); err != nil {
		panic(err)
	}
}
