package main

// Note: this is started internally by LocalAI and a server is allocated for each model

import (
	"flag"

	bloomz "github.com/go-skynet/LocalAI/pkg/backend/llm/bloomz"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &bloomz.LLM{}); err != nil {
		panic(err)
	}
}
