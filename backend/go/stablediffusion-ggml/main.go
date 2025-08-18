package main

import (
	"flag"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	gosd, err := purego.Dlopen("./libgosd.so", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	purego.RegisterLibFunc(&LoadModel, gosd, "load_model")
	purego.RegisterLibFunc(&GenImage, gosd, "gen_image")

	flag.Parse()

	if err := grpc.StartServer(*addr, &SDGGML{}); err != nil {
		panic(err)
	}
}
