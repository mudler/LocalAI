package main

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
	libName := os.Getenv("SAM3_LIBRARY")
	if libName == "" {
		libName = "./libgosam3-fallback.so"
	}

	gosamLib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}

	libFuncs := []LibFuncs{
		{&CppLoadModel, "sam3_cpp_load_model"},
		{&CppEncodeImage, "sam3_cpp_encode_image"},
		{&CppSegmentPVS, "sam3_cpp_segment_pvs"},
		{&CppSegmentPCS, "sam3_cpp_segment_pcs"},
		{&CppGetNDetections, "sam3_cpp_get_n_detections"},
		{&CppGetDetectionX, "sam3_cpp_get_detection_x"},
		{&CppGetDetectionY, "sam3_cpp_get_detection_y"},
		{&CppGetDetectionW, "sam3_cpp_get_detection_w"},
		{&CppGetDetectionH, "sam3_cpp_get_detection_h"},
		{&CppGetDetectionScore, "sam3_cpp_get_detection_score"},
		{&CppGetDetectionMaskPNG, "sam3_cpp_get_detection_mask_png"},
		{&CppFreeResults, "sam3_cpp_free_results"},
	}

	for _, lf := range libFuncs {
		purego.RegisterLibFunc(lf.FuncPtr, gosamLib, lf.Name)
	}

	flag.Parse()

	if err := grpc.StartServer(*addr, &SAM3{}); err != nil {
		panic(err)
	}
}
