package main

// cloud-proxy is a LocalAI backend that forwards request traffic to an
// external HTTP provider (OpenAI, Anthropic, etc.). Two modes:
//
//   - passthrough: serves the Forward RPC; the client wire format is
//     preserved end-to-end, no translation.
//   - translate: serves Predict/PredictStream; the backend converts
//     internal proto to the provider's wire format. (Phases 5–6.)
//
// LoadModel reads UpstreamURL/Mode/Provider/key references from
// ProxyOptions and resolves the API key once at load time.

import (
	"flag"
	"os"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"golang.org/x/term"
)

var addr = flag.String("addr", "localhost:50051", "the address to listen on")

func main() {
	// xlog's default handler emits ANSI color codes; that's fine for an
	// interactive shell but unreadable when the backend's stdout is
	// captured by LocalAI and tee'd to a log file. Force plain text when
	// LOCALAI_LOG_FORMAT is unset and stdout isn't a terminal.
	format := os.Getenv("LOCALAI_LOG_FORMAT")
	if format == "" && !term.IsTerminal(int(os.Stdout.Fd())) {
		format = xlog.TextFormat
	}
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv("LOCALAI_LOG_LEVEL")), format))
	flag.Parse()
	if err := grpc.StartServer(*addr, NewCloudProxy()); err != nil {
		panic(err)
	}
}
