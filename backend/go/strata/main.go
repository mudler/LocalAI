package main

// strata is an EXPERIMENTAL LocalAI backend for the Strata inference engine
// (https://github.com/Niko1221/Strata). It starts the engine executable in
// `--serve` mode and speaks its stdin/stdout line protocol. See README.md.

import (
	"flag"
	"os"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"golang.org/x/term"
)

var addr = flag.String("addr", "localhost:50051", "the address to listen on")

func main() {
	format := os.Getenv("LOCALAI_LOG_FORMAT")
	if format == "" && !term.IsTerminal(int(os.Stdout.Fd())) {
		format = xlog.TextFormat
	}
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv("LOCALAI_LOG_LEVEL")), format))
	flag.Parse()
	if err := grpc.StartServer(*addr, NewStrata()); err != nil {
		panic(err)
	}
}
