package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func registerLibFuncs(lib uintptr) {
	registerLibFuncsWith(func(fptr any, name string) {
		purego.RegisterLibFunc(fptr, lib, name)
	})
}

func main() {
	// run.sh selects the CPU-variant directory and points TRELLIS2_LIBRARY at it.
	libName := os.Getenv("TRELLIS2_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./variants/fallback/libtrellis2.dylib"
		} else {
			libName = "./variants/fallback/libtrellis2.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	registerLibFuncs(lib)

	if got := t2AbiVersion(); got != abiVersion {
		panic(fmt.Sprintf("trellis2 ABI mismatch: library reports %d, backend built for %d", got, abiVersion))
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &Trellis2{}); err != nil {
		panic(err)
	}
}
