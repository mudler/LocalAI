package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	vaeTiling    vaeTiling
}

var (
	LoadModel func(model, model_apth string, options []uintptr, threads int32, diff int) int
	GenImage  func(params uintptr, steps int, dst string, cfgScale float32, srcImage string, strength float32, maskImage string, refImages []uintptr, refImagesCount int) int
	GenVideo  func(params uintptr, steps int, dst string, cfgScale float32, fps int, initImage string, endImage string) int

	TilingParamsSetEnabled       func(params uintptr, enabled bool)
	TilingParamsSetTileSizes     func(params uintptr, tileSizeX int, tileSizeY int)
	TilingParamsSetRelSizes      func(params uintptr, relSizeX float32, relSizeY float32)
	TilingParamsSetTargetOverlap func(params uintptr, targetOverlap float32)

	ImgGenParamsNew                func() uintptr
	ImgGenParamsSetPrompts         func(params uintptr, prompt string, negativePrompt string)
	ImgGenParamsSetDimensions      func(params uintptr, width int, height int)
	ImgGenParamsSetSeed            func(params uintptr, seed int64)
	ImgGenParamsGetVaeTilingParams func(params uintptr) uintptr

	VidGenParamsNew            func() uintptr
	VidGenParamsSetPrompts     func(params uintptr, prompt string, negativePrompt string)
	VidGenParamsSetDimensions  func(params uintptr, width int, height int)
	VidGenParamsSetSeed        func(params uintptr, seed int64)
	VidGenParamsSetVideoFrames func(params uintptr, n int)
)

type vaeTiling struct {
	enabled       bool
	tileSizeX     int
	tileSizeY     int
	hasTileSize   bool
	targetOverlap float32
	hasOverlap    bool
}

func parseVAETiling(options []string) vaeTiling {
	var t vaeTiling
	for _, op := range options {
		name, value, hasValue := strings.Cut(op, ":")
		switch name {
		case "vae_tiling":
			// A bare flag reads as "on", matching "diffusion_model". The truthy
			// spellings are the ones load_model already accepts for its own
			// bool options, so an author does not have to remember two
			// conventions.
			t.enabled = !hasValue || value == "true" || value == "1"
		case "vae_tile_size":
			if x, y, ok := parseTileSize(value); ok {
				t.tileSizeX, t.tileSizeY, t.hasTileSize = x, y, true
			}
		case "vae_tile_overlap":
			if f, err := strconv.ParseFloat(value, 32); err == nil && f >= 0 {
				t.targetOverlap, t.hasOverlap = float32(f), true
			}
		}
	}
	return t
}

// parseTileSize accepts "512" for a square tile and "512x384" for a
// rectangular one.
//
// A value it cannot make sense of is reported as absent rather than as a zero.
// The caller only calls the upstream setter when a size was given, so a typo
// leaves the library's own default in place instead of installing a degenerate
// tiling that would fail at generation time.
func parseTileSize(value string) (int, int, bool) {
	xs, ys, split := strings.Cut(value, "x")
	if !split {
		ys = xs
	}
	x, err := strconv.Atoi(xs)
	if err != nil || x <= 0 {
		return 0, 0, false
	}
	y, err := strconv.Atoi(ys)
	if err != nil || y <= 0 {
		return 0, 0, false
	}
	return x, y, true
}

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
	// Read from the unfiltered list: none of the tiling options name a path, so
	// the resolution pass above neither rewrites nor drops them.
	sd.vaeTiling = parseVAETiling(opts.Options)

	ret := LoadModel(modelFile, modelPathC, options, opts.Threads, diffusionModel)
	runtime.KeepAlive(keepAlive)
	fmt.Fprintf(os.Stderr, "LoadModel: %d\n", ret)
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

	// At the time of writing Purego doesn't recurse into slices and convert Go strings to pointers so we need to do that
	var keepAlive []any
	refImagesCount := len(opts.RefImages)
	refImages := make([]uintptr, refImagesCount, refImagesCount+1)
	for i, ri := range opts.RefImages {
		bytep := CString(ri)
		refImages[i] = uintptr(unsafe.Pointer(bytep))
		keepAlive = append(keepAlive, bytep)
	}

	// Default strength for img2img (0.75 is a good default)
	strength := float32(0.75)

	// free'd by GenImage
	p := ImgGenParamsNew()
	ImgGenParamsSetPrompts(p, t, negative)
	ImgGenParamsSetDimensions(p, int(opts.Width), int(opts.Height))
	ImgGenParamsSetSeed(p, int64(opts.Seed))
	// Tiling decodes the latent in overlapping tiles, so the VAE compute buffer
	// scales with the tile rather than with the image. That is the difference
	// between working and failing on any device that caps a single allocation
	// (RADV reports a 4GiB maxMemoryAllocationSize, for one) or that simply
	// does not have the VRAM for a full-frame decode at high resolution.
	//
	// Only the setters the operator configured are called, so an unset tile
	// size or overlap keeps the library's own default.
	vaep := ImgGenParamsGetVaeTilingParams(p)
	TilingParamsSetEnabled(vaep, sd.vaeTiling.enabled)
	if sd.vaeTiling.hasTileSize {
		TilingParamsSetTileSizes(vaep, sd.vaeTiling.tileSizeX, sd.vaeTiling.tileSizeY)
	}
	if sd.vaeTiling.hasOverlap {
		TilingParamsSetTargetOverlap(vaep, sd.vaeTiling.targetOverlap)
	}

	ret := GenImage(p, int(opts.Step), dst, sd.cfgScale, srcImage, strength, maskImage, refImages, refImagesCount)
	runtime.KeepAlive(keepAlive)
	fmt.Fprintf(os.Stderr, "GenImage: %d\n", ret)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}

func (sd *SDGGML) GenerateVideo(opts *pb.GenerateVideoRequest) error {
	dst := opts.Dst
	if dst == "" {
		return fmt.Errorf("dst is empty")
	}

	width := int(opts.Width)
	height := int(opts.Height)
	if width == 0 {
		width = 512
	}
	if height == 0 {
		height = 512
	}

	numFrames := int(opts.NumFrames)
	if numFrames <= 0 {
		numFrames = 16
	}

	fps := int(opts.Fps)
	if fps <= 0 {
		fps = 16
	}

	steps := int(opts.Step)
	if steps <= 0 {
		steps = 20
	}

	cfg := opts.CfgScale
	if cfg == 0 {
		cfg = sd.cfgScale
	}
	if cfg == 0 {
		cfg = 5.0
	}

	// sd_vid_gen_params_new allocates; gen_video frees it after the generation call.
	p := VidGenParamsNew()
	VidGenParamsSetPrompts(p, opts.Prompt, opts.NegativePrompt)
	VidGenParamsSetDimensions(p, width, height)
	VidGenParamsSetSeed(p, int64(opts.Seed))
	VidGenParamsSetVideoFrames(p, numFrames)

	fmt.Fprintf(os.Stderr, "GenerateVideo: dst=%s size=%dx%d frames=%d fps=%d steps=%d cfg=%.2f\n",
		dst, width, height, numFrames, fps, steps, cfg)

	ret := GenVideo(p, steps, dst, cfg, fps, opts.StartImage, opts.EndImage)
	if ret != 0 {
		return fmt.Errorf("video inference failed (code %d)", ret)
	}
	return nil
}
