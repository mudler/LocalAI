package main

// colibri is an EXPERIMENTAL LocalAI backend for colibri
// (https://github.com/JustVugg/colibri), an engine that runs very large
// mixture-of-experts models with the experts streamed from disk. It starts
// colibri's `coli serve` gateway on a loopback port and translates the gRPC
// calls to its OpenAI-compatible API. See docs/content/models/engines/colibri.md.

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

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

	b := NewColibri()
	// LocalAI stops a backend with SIGTERM; stop coli serve and its engine
	// first, so no model stays loaded behind it.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		_ = b.Free()
		os.Exit(0)
	}()

	if err := grpc.StartServer(*addr, b); err != nil {
		panic(err)
	}
}
