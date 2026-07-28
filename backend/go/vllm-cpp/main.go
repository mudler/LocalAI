package main

// Note: this is started internally by LocalAI and a server is allocated for each model
import (
	"flag"
	"os"
	"runtime"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	libName := os.Getenv("VLLM_CPP_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./libvllm.dylib"
		} else {
			libName = "./libvllm.so"
		}
	}

	if err := registerLib(libName); err != nil {
		panic(err)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &VllmCpp{}); err != nil {
		panic(err)
	}
}
