package main

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

var (
	LoadModel func(model, model_apth string, options []string, threads int32, diff int) int
	GenImage func(text, negativeText string, width, height, steps int, seed int64, dst string, cfgScale float32, srcImage string, strength float32, maskImage string, refImages []string, refImagesCount int) int
)

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

	options := make([]string, len(oo), len(oo) + 1)
	*(*uintptr)(unsafe.Add(unsafe.Pointer(&options), uintptr(len(oo)))) = 0

	sd.cfgScale = opts.CFGScale

	ret := LoadModel(modelFile, modelPathC, options, opts.Threads, diffusionModel)
	if ret != 0 {
		return fmt.Errorf("could not load model")
	}

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
	refImages := make([]string, refImagesCount, refImagesCount + 1)
	copy(refImages, opts.RefImages)
	*(*uintptr)(unsafe.Add(unsafe.Pointer(&refImages), refImagesCount)) = 0

	// Default strength for img2img (0.75 is a good default)
	strength := float32(0.75)

	ret := GenImage(t, negative, int(opts.Width), int(opts.Height), int(opts.Step), int64(opts.Seed), dst, sd.cfgScale, srcImage, strength, maskImage, refImages, refImagesCount)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
