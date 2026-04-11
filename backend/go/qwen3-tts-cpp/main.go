package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"
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
	// Get library name from environment variable, default to fallback
	libName := os.Getenv("QWEN3TTS_LIBRARY")
	if libName == "" {
		libName = "./libgoqwen3ttscpp-fallback.so"
	}

	gosd, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoadModel, "load_model"},
		{&CppSynthesize, "synthesize"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosd, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &Qwen3TtsCpp{}); err != nil {
		panic(err)
	}
}
