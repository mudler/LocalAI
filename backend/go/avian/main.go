package main

// Avian backend - proxies requests to the Avian API (https://api.avian.io/v1)
// Avian provides an OpenAI-compatible API for LLM inference.

import (
	"flag"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &Avian{}); err != nil {
		panic(err)
	}
}
