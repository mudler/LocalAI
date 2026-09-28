// SPDX-License-Identifier: MIT
package main

import (
	"flag"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "gRPC listen address")
	flag.Parse()
	if err := grpc.StartServer(*addr, &Pixal3D{}); err != nil {
		panic(err)
	}
}
