package main

// Note: this is started internally by LocalAI and a server is allocated for each model

import (
	"flag"

	rwkv "github.com/go-skynet/LocalAI/pkg/backend/llm/rwkv"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &rwkv.LLM{}); err != nil {
		panic(err)
	}
}
