package main

// #cgo CXXFLAGS: -I${SRCDIR}/sources/stablediffusion-ggml.cpp/thirdparty -I${SRCDIR}/sources/stablediffusion-ggml.cpp -I${SRCDIR}/sources/stablediffusion-ggml.cpp/ggml/include
// #cgo LDFLAGS: -L${SRCDIR}/ -lsd -lstdc++ -lm -lggmlall -lgomp
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

	modelPath := opts.ModelPath

	modelFile := C.CString(opts.ModelFile)
	defer C.free(unsafe.Pointer(modelFile))

	modelPathC := C.CString(modelPath)
	defer C.free(unsafe.Pointer(modelPathC))

	var options **C.char
	// prepare the options array to pass to C

	size := C.size_t(unsafe.Sizeof((*C.char)(nil)))
	length := C.size_t(len(opts.Options))
	options = (**C.char)(C.malloc((length + 1) * size))
	view := (*[1 << 30]*C.char)(unsafe.Pointer(options))[0 : len(opts.Options)+1 : len(opts.Options)+1]

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
	view[len(oo)] = nil

	sd.cfgScale = opts.CFGScale

	ret := C.load_model(modelFile, modelPathC, options, C.int(opts.Threads), C.int(diffusionModel))
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

	// Handle source image path
	var srcImage *C.char
	if opts.Src != "" {
		srcImage = C.CString(opts.Src)
		defer C.free(unsafe.Pointer(srcImage))
	}

	// Handle mask image path
	var maskImage *C.char
	if opts.EnableParameters != "" {
		// Parse EnableParameters for mask path if provided
		// This is a simple approach - in a real implementation you might want to parse JSON
		if strings.Contains(opts.EnableParameters, "mask:") {
			parts := strings.Split(opts.EnableParameters, "mask:")
			if len(parts) > 1 {
				maskPath := strings.TrimSpace(parts[1])
				if maskPath != "" {
					maskImage = C.CString(maskPath)
					defer C.free(unsafe.Pointer(maskImage))
				}
			}
		}
	}

	// Handle reference images
	var refImages **C.char
	var refImagesCount C.int
	if len(opts.RefImages) > 0 {
		refImagesCount = C.int(len(opts.RefImages))
		// Allocate array of C strings
		size := C.size_t(unsafe.Sizeof((*C.char)(nil)))
		refImages = (**C.char)(C.malloc((C.size_t(len(opts.RefImages)) + 1) * size))
		view := (*[1 << 30]*C.char)(unsafe.Pointer(refImages))[0 : len(opts.RefImages)+1 : len(opts.RefImages)+1]

		for i, refImagePath := range opts.RefImages {
			view[i] = C.CString(refImagePath)
			defer C.free(unsafe.Pointer(view[i]))
		}
		view[len(opts.RefImages)] = nil
	}

	// Default strength for img2img (0.75 is a good default)
	strength := C.float(0.75)
	if opts.Src != "" {
		// If we have a source image, use img2img mode
		// You could also parse strength from EnableParameters if needed
		strength = C.float(0.75)
	}

	ret := C.gen_image(t, negative, C.int(opts.Width), C.int(opts.Height), C.int(opts.Step), C.int(opts.Seed), dst, C.float(sd.cfgScale), srcImage, strength, maskImage, refImages, refImagesCount)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
