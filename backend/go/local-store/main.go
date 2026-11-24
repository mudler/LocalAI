package main

// Note: this is started internally by LocalAI and a server is allocated for each store

import (
	"flag"
	"os"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv("LOCALAI_LOG_LEVEL")), os.Getenv("LOCALAI_LOG_FORMAT")))

	flag.Parse()

	if err := grpc.StartServer(*addr, NewStore()); err != nil {
		panic(err)
	}
}
