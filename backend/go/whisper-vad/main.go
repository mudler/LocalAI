package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"
	"fmt"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func getLibrary() string {
	switch runtime.GOOS {
	case "linux":
		return "./libgowhisper.so"
	default:
		panic(fmt.Errorf("GOOS=%s is not supported", runtime.GOOS))
	}
}

func main() {
	gosd, err := purego.Dlopen(getLibrary(), purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&CppLoadModel, gosd, "load_model")
	purego.RegisterLibFunc(&CppVAD, gosd, "vad")

	flag.Parse()

	if err := grpc.StartServer(*addr, &Whisper{}); err != nil {
		panic(err)
	}
}
