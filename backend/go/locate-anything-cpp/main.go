package main

// main.go - entry point for the locate-anything-cpp gRPC backend.
//
// Dlopens liblocateanythingcpp-<variant>.so via purego at the path in
// LOCATEANYTHING_LIBRARY (set by run.sh based on /proc/cpuinfo), registers
// the la_capi_* C ABI symbols, then starts the gRPC server.

import (
	"flag"
	"os"
	"runtime"

	"github.com/ebitengine/purego"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

var (
	addr = flag.String("addr", "localhost:50051", "the address to connect to")
)

type LibFuncs struct {
	FuncPtr any
	Name    string
}

func main() {
	// Get library name from environment variable, default to fallback
	libName := os.Getenv("LOCATEANYTHING_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./liblocateanythingcpp-fallback.dylib"
		} else {
			libName = "./liblocateanythingcpp-fallback.so"
		}
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CapiLoad, "la_capi_load"},
		{&CapiFree, "la_capi_free"},
		{&CapiLocatePath, "la_capi_locate_path"},
		{&CapiLocateBuffer, "la_capi_locate_buffer"},
		{&CapiGetNDetections, "la_capi_get_n_detections"},
		{&CapiGetDetectionBox, "la_capi_get_detection_box"},
		{&CapiGetDetectionLabel, "la_capi_get_detection_label"},
		{&CapiFreeString, "la_capi_free_string"},
		{&CapiLastError, "la_capi_last_error"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &LocateAnythingCpp{}); err != nil {
		panic(err)
	}
}
