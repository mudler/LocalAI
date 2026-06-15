package main

// Started internally by LocalAI; a server is allocated per model.

import (
	"flag"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	ort "github.com/yalue/onnxruntime_go"
)

var addr = flag.String("addr", "localhost:50051", "the address to connect to")

func main() {
	flag.Parse()

	// InitializeONNXRuntime reads ONNXRUNTIME_LIB_PATH (set by run.sh) and
	// dlopens libonnxruntime before any session is created in Load().
	if err := InitializeONNXRuntime(); err != nil {
		panic(err)
	}
	defer func() { _ = ort.DestroyEnvironment() }()

	if err := grpc.StartServer(*addr, &SupertonicBackend{}); err != nil {
		panic(err)
	}
}
