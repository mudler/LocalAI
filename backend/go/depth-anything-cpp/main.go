package main

// main.go - entry point for the depth-anything-cpp gRPC backend.
//
// Dlopens libdepthanythingcpp-<variant>.so via purego at the path in
// DEPTHANYTHING_LIBRARY (set by run.sh based on /proc/cpuinfo), registers the
// da_capi_* C ABI symbols, then starts the gRPC server.

import (
	"flag"
	"os"

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
	libName := os.Getenv("DEPTHANYTHING_LIBRARY")
	if libName == "" {
		libName = "./libdepthanythingcpp-fallback.so"
	}

	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CapiLoad, "da_capi_load"},
		{&CapiFree, "da_capi_free"},
		{&CapiLastError, "da_capi_last_error"},
		{&CapiDepthPath, "da_capi_depth_path"},
		{&CapiFreeFloats, "da_capi_free_floats"},
		{&CapiPosePath, "da_capi_pose_path"},
		{&CapiDepthDense, "da_capi_depth_dense"},
		{&CapiPoints, "da_capi_points"},
		{&CapiFreeBytes, "da_capi_free_bytes"},
		{&CapiExportGlb, "da_capi_export_glb"},
		{&CapiExportColmap, "da_capi_export_colmap"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, lib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &DepthAnythingCpp{}); err != nil {
		panic(err)
	}
}
