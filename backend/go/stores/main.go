package main

// Note: this is started internally by LocalAI and a server is allocated for each store

import (
	"flag"
	"os"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	flag.Parse()

	if err := grpc.StartServer(*addr, NewStore()); err != nil {
		panic(err)
	}
}
