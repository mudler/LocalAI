package main

// #cgo CXXFLAGS: -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/thirdparty -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp -I${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/ggml/include
// #cgo LDFLAGS: -L${SRCDIR}/ -L${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/build/ggml/src/ggml-cpu -L${SRCDIR}/../../../../sources/stablediffusion-ggml.cpp/build/ggml/src -lsd -lstdc++ -lm -lggml -lggml-base -lggml-cpu -lgomp
// #include <gosd.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

type SDGGML struct {
	base.SingleThread
	threads      int
	sampleMethod string
	cfgScale     float32
}

func (sd *SDGGML) Load(opts *pb.ModelOptions) error {

	sd.threads = int(opts.Threads)

	modelFile := C.CString(opts.ModelFile)
	defer C.free(unsafe.Pointer(modelFile))

	var options **C.char
	// prepare the options array to pass to C

	size := C.size_t(unsafe.Sizeof((*C.char)(nil)))
	length := C.size_t(len(opts.Options))
	options = (**C.char)(C.malloc(length * size))
	view := (*[1 << 30]*C.char)(unsafe.Pointer(options))[0:len(opts.Options):len(opts.Options)]

	var diffusionModel int

	var oo []string
	for _, op := range opts.Options {
		if op == "diffusion_model" {
			diffusionModel = 1
			continue
		}

		// If it's an option path, we resolve absolute path from the model path
		if strings.Contains(op, ":") && strings.Contains(op, "path") {
			data := strings.Split(op, ":")
			data[1] = filepath.Join(opts.ModelPath, data[1])
			if err := utils.VerifyPath(data[1], opts.ModelPath); err == nil {
				oo = append(oo, strings.Join(data, ":"))
			}
		} else {
			oo = append(oo, op)
		}
	}

	fmt.Fprintf(os.Stderr, "Options: %+v\n", oo)

	for i, x := range oo {
		view[i] = C.CString(x)
	}

	sd.cfgScale = opts.CFGScale

	ret := C.load_model(modelFile, options, C.int(opts.Threads), C.int(diffusionModel))
	if ret != 0 {
		return fmt.Errorf("could not load model")
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

	ret := C.gen_image(t, negative, C.int(opts.Width), C.int(opts.Height), C.int(opts.Step), C.int(opts.Seed), dst, C.float(sd.cfgScale))
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
