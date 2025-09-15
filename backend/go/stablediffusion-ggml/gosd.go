package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

var (
	LoadModel func(model, model_apth string, options []uintptr, threads int32, diff int) int
	GenImage  func(params uintptr, steps int, dst string, cfgScale float32, srcImage string, strength float32, maskImage string, refImages []string, refImagesCount int) int

	TilingParamsSetEnabled       func(params uintptr, enabled bool)
	TilingParamsSetTileSizes     func(params uintptr, tileSizeX int, tileSizeY int)
	TilingParamsSetRelSizes      func(params uintptr, relSizeX float32, relSizeY float32)
	TilingParamsSetTargetOverlap func(params uintptr, targetOverlap float32)

	ImgGenParamsNew                func() uintptr
	ImgGenParamsSetPrompts         func(params uintptr, prompt string, negativePrompt string)
	ImgGenParamsSetDimensions      func(params uintptr, width int, height int)
	ImgGenParamsSetSeed            func(params uintptr, seed int64)
	ImgGenParamsGetVaeTilingParams func(params uintptr) uintptr
)

// Copied from Purego internal/strings
// TODO: We should upstream sending []string
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func CString(name string) *byte {
	if hasSuffix(name, "\x00") {
		return &(*(*[]byte)(unsafe.Pointer(&name)))[0]
	}
	b := make([]byte, len(name)+1)
	copy(b, name)
	return &b[0]
}

func (sd *SDGGML) Load(opts *pb.ModelOptions) error {

	sd.threads = int(opts.Threads)

	modelPath := opts.ModelPath

	modelFile := opts.ModelFile
	modelPathC := modelPath

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

	// At the time of writing Purego doesn't recurse into slices and convert Go strings to pointers so we need to do that
	var keepAlive []any
	options := make([]uintptr, len(oo), len(oo)+1)
	for i, op := range oo {
		bytep := CString(op)
		options[i] = uintptr(unsafe.Pointer(bytep))
		keepAlive = append(keepAlive, bytep)
	}

	sd.cfgScale = opts.CFGScale

	ret := LoadModel(modelFile, modelPathC, options, opts.Threads, diffusionModel)
	if ret != 0 {
		return fmt.Errorf("could not load model")
	}

	runtime.KeepAlive(keepAlive)

	return nil
}

func (sd *SDGGML) GenerateImage(opts *pb.GenerateImageRequest) error {
	t := opts.PositivePrompt
	dst := opts.Dst
	negative := opts.NegativePrompt
	srcImage := opts.Src

	var maskImage string
	if opts.EnableParameters != "" {
		if strings.Contains(opts.EnableParameters, "mask:") {
			parts := strings.Split(opts.EnableParameters, "mask:")
			if len(parts) > 1 {
				maskPath := strings.TrimSpace(parts[1])
				if maskPath != "" {
					maskImage = maskPath
				}
			}
		}
	}

	refImagesCount := len(opts.RefImages)
	refImages := make([]string, refImagesCount, refImagesCount+1)
	copy(refImages, opts.RefImages)
	*(*uintptr)(unsafe.Add(unsafe.Pointer(&refImages), refImagesCount)) = 0

	// Default strength for img2img (0.75 is a good default)
	strength := float32(0.75)

	// free'd by GenImage
	p := ImgGenParamsNew()
	ImgGenParamsSetPrompts(p, t, negative)
	ImgGenParamsSetDimensions(p, int(opts.Width), int(opts.Height))
	ImgGenParamsSetSeed(p, int64(opts.Seed))
	vaep := ImgGenParamsGetVaeTilingParams(p)
	TilingParamsSetEnabled(vaep, false)

	ret := GenImage(p, int(opts.Step), dst, sd.cfgScale, srcImage, strength, maskImage, refImages, refImagesCount)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
