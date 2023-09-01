package main

// GRPC Falcon server

// Note: this is started internally by LocalAI and a server is allocated for each model

import (
	"flag"

	llama "github.com/go-skynet/LocalAI/pkg/backend/llm/llama"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &llama.LLM{}); err != nil {
		panic(err)
	}
}
