package main

// main.go - entry point for the rfdetr-cpp gRPC backend.
//
// Dlopens librfdetrcpp-<variant>.so via purego at the path in
// RFDETR_LIBRARY (set by run.sh based on /proc/cpuinfo), registers the
// rfdetr_capi_* C ABI symbols, then starts the gRPC server.

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
	libName := os.Getenv("RFDETR_LIBRARY")
	if libName == "" {
		if runtime.GOOS == "darwin" {
			libName = "./librfdetrcpp-fallback.dylib"
		} else {
			libName = "./librfdetrcpp-fallback.so"
		}
	}

	rfdetrLib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CapiLoad, "rfdetr_capi_load"},
		{&CapiUnload, "rfdetr_capi_unload"},
		{&CapiDetectPath, "rfdetr_capi_detect_path"},
		{&CapiDetectBuffer, "rfdetr_capi_detect_buffer"},
		{&CapiFreeString, "rfdetr_capi_free_string"},
		{&CapiGetNDetections, "rfdetr_capi_get_n_detections"},
		{&CapiGetDetectionClassID, "rfdetr_capi_get_detection_class_id"},
		{&CapiGetDetectionBox, "rfdetr_capi_get_detection_box"},
		{&CapiGetDetectionScore, "rfdetr_capi_get_detection_score"},
		{&CapiGetDetectionClassName, "rfdetr_capi_get_detection_class_name"},
		{&CapiGetDetectionMaskPNG, "rfdetr_capi_get_detection_mask_png"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, rfdetrLib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &RFDetrCpp{}); err != nil {
		panic(err)
	}
}
