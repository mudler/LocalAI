package main

// Started internally by LocalAI - one gRPC server per loaded model.
//
// Loads libtimestral.so via purego and registers the flat C-API entry points
// declared in timestral_capi.h. The library name can be overridden with
// TIMESTRAL_LIBRARY (the same convention as the other purego backends); the
// default looks for the .so next to this binary, which run.sh puts on
// LD_LIBRARY_PATH.
import (
	"flag"
	"fmt"
	"os"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

func main() {
	libName := os.Getenv("TIMESTRAL_LIBRARY")
	if libName == "" {
		libName = "libtimestral.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(fmt.Errorf("timestral-cpp: dlopen %q: %w", libName, err))
	}
	registerLibFuncs(lib)

	flag.Parse()

	if err := grpc.StartServer(*addr, &TimestralCpp{}); err != nil {
		panic(err)
	}
}
