// SPDX-License-Identifier: MIT
package main

import (
	"flag"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"os"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "gRPC listen address")
	flag.Parse()
	if err := loadNativeLibrary(os.Getenv("GEMX_LIBRARY")); err != nil {
		xlog.Error("loading GEM-X library", "error", err)
		os.Exit(1)
	}
	if err := grpc.StartServer(*addr, &GemX{}); err != nil {
		xlog.Error("GEM-X server", "error", err)
		os.Exit(1)
	}
}
