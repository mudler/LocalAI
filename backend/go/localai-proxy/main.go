package main

// localai-proxy is a LocalAI backend that serves backend gRPC methods by
// calling the REST API of another LocalAI instance. It lets a model config
// (and so a failover chain target) live on a remote LocalAI while callers
// keep using the local backend interface for every modality, not only chat.

import (
	"flag"
	"os"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"golang.org/x/term"
)

var addr = flag.String("addr", "localhost:50051", "the address to listen on")

func main() {
	// xlog's default handler emits ANSI color codes, which are unreadable once
	// LocalAI captures the backend's stdout into a log file. Force plain text
	// when LOCALAI_LOG_FORMAT is unset and stdout is not a terminal.
	format := os.Getenv("LOCALAI_LOG_FORMAT")
	if format == "" && !term.IsTerminal(int(os.Stdout.Fd())) {
		format = xlog.TextFormat
	}
	xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv("LOCALAI_LOG_LEVEL")), format))
	flag.Parse()
	if err := grpc.StartServer(*addr, NewLocalAIProxy()); err != nil {
		panic(err)
	}
}
