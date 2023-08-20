package main

// Note: this is started internally by LocalAI and a server is allocated for each model

import (
	"flag"

	gpt4all "github.com/go-skynet/LocalAI/pkg/backend/llm/gpt4all"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &gpt4all.LLM{}); err != nil {
		panic(err)
	}
}
