package main

// #cgo CXXFLAGS: -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/thirdparty -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/ggml/include
// #cgo LDFLAGS: -L${SRCDIR}/ -L${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/build/ggml/src/ggml-cpu -L${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/build/ggml/src -lsd -lstdc++ -lm -lggml -lggml-base -lggml-cpu -lgomp
// #include <gosd.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type SDGGML struct {
	base.SingleThread
	threads int
}

func (sd *SDGGML) Load(opts *pb.ModelOptions) error {

	sd.threads = int(opts.Threads)

	schedulerType := C.CString(opts.SchedulerType)
	defer C.free(unsafe.Pointer(schedulerType))

	modelFile := C.CString(opts.ModelFile)
	defer C.free(unsafe.Pointer(modelFile))

	ret := C.load_model(modelFile, schedulerType, C.int(opts.Threads))
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}

func (sd *SDGGML) GenerateImage(opts *pb.GenerateImageRequest) error {
	t := C.CString(opts.PositivePrompt)
	defer C.free(unsafe.Pointer(t))

	dst := C.CString(opts.Dst)
	defer C.free(unsafe.Pointer(dst))

	negative := C.CString(opts.NegativePrompt)
	defer C.free(unsafe.Pointer(negative))

	sampleMethod := C.CString(opts.EnableParameters)
	defer C.free(unsafe.Pointer(sampleMethod))

	ret := C.gen_image(t, negative, C.int(opts.Width), C.int(opts.Height), C.int(opts.Step), C.int(opts.Seed), sampleMethod, dst)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
