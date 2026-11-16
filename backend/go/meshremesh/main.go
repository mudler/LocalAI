// SPDX-License-Identifier: MIT
package main

import (
	"flag"
	"os"

	"github.com/mudler/LocalAI/pkg/grpc"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "gRPC listen address")
	flag.Parse()
	if err := loadNativeLibrary(os.Getenv("MESHREMESH_LIBRARY")); err != nil {
		panic(err)
	}
	if err := grpc.StartServer(*addr, &MeshRemesh{}); err != nil {
		panic(err)
	}
}
