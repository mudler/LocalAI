package main

import (
	"flag"

	llama "github.com/go-skynet/LocalAI/pkg/backend/llm/llama-stable"

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
