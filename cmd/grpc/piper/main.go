package main

// Note: this is started internally by LocalAI and a server is allocated for each model

import (
	"flag"

	tts "github.com/go-skynet/LocalAI/pkg/backend/tts"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	flag.Parse()

	if err := grpc.StartServer(*addr, &tts.Piper{}); err != nil {
		panic(err)
	}
}
