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
	libName := os.Getenv("ACESTEP_LIBRARY")
	if libName == "" {
		libName = "./libgoacestepcpp-fallback.so"
	}

	gosd, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoadModel, "load_model"},
		{&CppGenerateMusic, "generate_music"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosd, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &AceStepCpp{}); err != nil {
		panic(err)
	}
}
